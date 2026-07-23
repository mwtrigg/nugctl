# `nugctl feed verify` — design

## Context

nugctl is gaining a conformance-oracle mode to validate NuGet v3 server
implementations — primarily an upcoming registry called Barn, incidentally
also the BaGetter instance it replaces. `nugctl feed verify` runs a battery
of read-only, negative, and (optionally) round-trip checks against a feed's
service index and reports pass/fail with a CI-friendly exit code.

## Command

```
nugctl feed verify [--push] [--package <id>]
```

Reuses existing global/persistent flags rather than inventing new ones:
`--profile`/`-p`, `--url`, `--api-key`, `--basic-auth-user`,
`--basic-auth-pass`, `-o/--output` (table|json|yaml), `--insecure`,
`--no-cache`, `-v/--verbose`. No bespoke `--json` flag — `-o json` already
does this everywhere else in the codebase.

New flags:
- `--push` (bool, default false) — also run the round-trip checks (push,
  poll, download, unlist). Off by default because it requires a writable
  feed and leaves state behind.
- `--package <id>` (string, default "") — target package for the
  registration/flat-container/hash checks. Default: first hit of an
  unfiltered search (`c.Search("", 0, 1, false)`).

## Package layout

```
internal/verify/
  verify.go     — Check, Status, Report types; Run() orchestrator; ExitCode()
  readonly.go   — resource-presence/probe, package-integrity, negative checks
  push.go       — --push round-trip
  nupkg.go      — minimal in-memory .nupkg builder
  *_test.go     — table-driven, httptest-backed, no network

cmd/
  feed_verify.go — cobra wiring, flag parsing, report printing, os.Exit
```

`internal/verify` has no dependency on `cmd` or cobra — it takes a
`*client.Client` and returns a `*Report`, so it's testable standalone.

### Client additions (`internal/client/nuget.go`)

Following the existing `withResource` pattern:

- `FlatContainerVersions(id string) (*FlatContainerVersions, error)` — GETs
  `{PackageBaseAddress}/{id-lower}/index.json`.
- `PullBytes(id, version string) ([]byte, error)` — download without
  writing to disk. `Pull` is refactored to call this and write the file,
  so behavior is unchanged for existing callers.
- `PushBytes(filename string, data []byte) error` — push from an in-memory
  buffer. `Push` is refactored to read the file and call this.

## Data model

```go
type Status string
const (
    StatusPass Status = "pass"
    StatusFail Status = "fail"
    StatusWarn Status = "warn" // non-fatal but worth surfacing (e.g. delete unsupported)
    StatusSkip Status = "skip" // couldn't run (e.g. no package available, field absent)
)

type Check struct {
    Name     string
    Category string // "readonly" | "negative" | "push"
    Status   Status
    Detail   string
    Err      string `json:",omitempty"`
}

type Report struct {
    FeedURL     string
    Checks      []Check
    Aborted     bool   // fatal setup failure — couldn't even fetch the service index
    AbortReason string
}
```

`Report.ExitCode()`: `2` if `Aborted`; else `1` if any `Check.Status ==
StatusFail`; else `0`. `warn`/`skip` never affect the exit code.

## Checks

### Read-only (always run)

1. **Fetch service index.** Any failure here (network, TLS, 401/403, 5xx,
   malformed JSON) sets `Aborted = true` and stops the run — nothing
   downstream can execute without it. This is the only fatal path; exit 2
   is reserved for it.
2. **Resource presence**, one check per required type: `SearchQueryService`,
   `RegistrationsBaseUrl`, `PackageBaseAddress`, `PackagePublish/2.0.0`
   (prefix match, matching `Client.resourceURL`'s own matching rule).
3. **Resource responds**, where a safe read exists:
   - `SearchQueryService`: live query (`q=""`, `take=1`), expect 200 +
     decodable body.
   - `RegistrationsBaseUrl` / `PackageBaseAddress`: exercised by the
     package-integrity checks below rather than a separate synthetic probe.
   - `PackagePublish/2.0.0`: **no read-only probe exists** (PUT-only
     endpoint). This check is `skip`ped in read-only mode with a detail
     explaining it's only exercised by `--push`. Documented as a known gap
     rather than faked.
4. **Package integrity**, target = `--package` or the first hit of an
   unfiltered search:
   - No target resolvable (empty feed, no `--package`) → all checks in
     this group are `skip`, not `fail`.
   - Registration structurally sound: `count > 0`, ≥1 page, each leaf's
     `catalogEntry.id` and `catalogEntry.version` non-empty. These are the
     only two fields the NuGet v3 registration spec actually mandates at
     the leaf level — everything else (`authors`, `tags`, `published`,
     `licenseUrl`, ...) is informational and not asserted.
   - Flat-container version list (`FlatContainerVersions`) matches the set
     of versions found in the registration (case-insensitive compare).
   - Download the package (`PullBytes`), compute SHA-512, base64-encode,
     compare to `catalogEntry.packageHash` **only if that field is
     non-empty** — it's optional per `omitempty` in `CatalogEntry`, so its
     absence is `skip`, not `fail`.

### Negative (always run)

5. **404 shape.** `Registration("nugctl-verify-does-not-exist-<rand>")`
   must categorize as `CatNotFound`. Any other outcome (200, 500, empty
   200) is a `fail` with the actual status/category in `Detail`.
6. **Auth rejection**, only if the resolved profile carries credentials
   (API key or Basic Auth): `DELETE` the same nonexistent id using
   deliberately garbled credentials, expect `CatUnauthorized`. Chosen over
   a garbage-credentialed push specifically because it has zero
   side-effect risk — there's nothing to delete regardless of how the
   server responds, so even a maximally noncompliant server can't be
   tricked into accepting content. `CatUnauthorized` → `pass`. The v3 spec
   doesn't mandate whether a server checks existence or auth first, so a
   server that checks existence first will legitimately 404 here with the
   garbled credentials never evaluated — that's `warn`, not `fail`: the
   probe is inconclusive, not evidence the server accepts bad credentials.
   Anything else is `fail`.

### Push round-trip (only with `--push`)

7. Build a minimal `.nupkg` in memory: `{id}.nuspec` + one content file,
   zipped. Deliberately **not** full OPC-compliant (no `_rels`,
   `[Content_Types].xml`, core-properties psmdcp) — that's what
   BaGetter-class self-hosted feeds actually parse (bare zip + nuspec at
   root). A stricter build would be needed to satisfy nuget.org itself,
   which is out of scope here. `id = nugctl-verify-<unix-timestamp>`,
   version `1.0.0`.
8. `PushBytes` it.
9. Poll every 1s, up to 90s, until the id appears in both search and
   registration. Timeout → `fail` with elapsed time in `Detail`. These
   values are hardcoded, not flags — see decisions below. 90s is
   deliberately well above a plausible index-regen debounce window on the
   target feed (e.g. a 30s quiet-period default before regeneration) —
   setting the oracle's timeout equal to the target's own debounce default
   would make every push round-trip a coin-flip flake.
10. Download (`PullBytes`) and verify SHA-512 against the pushed bytes.
11. `Delete()` it (the codebase's single unlist/hard-delete endpoint).
    Poll (same interval/timeout) until absent from default search.
12. Assert exact-version download still succeeds — per the NuGet v3 spec,
    unlist hides from search but keeps direct download working. If the
    download instead now 404s, the feed hard-deleted rather than
    unlisted.
13. **Delete-support conclusion**, derived from step 12 rather than an
    extra call: still downloadable → `warn`, "feed does not support hard
    delete; test package left unlisted on the feed" (loud but non-fatal,
    per your call — this run's target feed is Barn's own dev instance, so
    leftover test packages aren't a real-world pollution concern, but the
    signal is still useful). No longer downloadable → `pass`, "feed
    supports hard delete."

## Output

Default (table): one row per check — `CATEGORY | NAME | STATUS | DETAIL`,
grouped in the order above. Summary line above the table:
`X passed, Y failed, Z warned, W skipped`.

`-o json` / `-o yaml`: the full `Report` struct.

## Exit codes

- `0` — ran to completion, no `fail` checks (warns/skips don't count).
- `1` — ran to completion, at least one `fail`.
- `2` — `Aborted` (couldn't fetch the service index at all).

This is the one place `feed verify` deviates from the rest of the
codebase's command convention: every other `RunE` returns an `error` and
lets `Execute()` in `cmd/root.go` turn any non-nil error into a blanket
`os.Exit(1)`. `feed verify` instead always prints its report (even on
partial/aborted runs) and calls `os.Exit(report.ExitCode())` directly,
because the 3-way exit contract is the actual point of the command — it's
meant to run in CI.

## Testing

Table-driven tests in `internal/verify`, each spinning up an
`httptest.NewServer`:

- **Compliant feed** fixture — all resources present, well-formed
  registration, matching flat-container list, correct hash → expect all
  `pass`, exit 0.
- **Missing-resource feed** fixture — service index omits e.g.
  `PackageBaseAddress` → expect that presence check `fail`, dependent
  package-integrity checks `skip` (can't reach a base URL that isn't
  advertised), exit 1.
- **Hash-mismatch feed** fixture — registration's `packageHash` doesn't
  match the served `.nupkg` bytes → expect that one check `fail`, exit 1.

Push round-trip tests use a small stateful `http.ServeMux` fake (in-memory
package store) covering push/poll/download/unlist/re-download, exercised
directly against `internal/verify` — no cobra, no real network anywhere.

## Decisions not dictated by the spec

- **Minimal `.nupkg` construction**: bare zip with `{id}.nuspec` at root
  plus one content file — not full OPC compliance. Targets what
  self-hosted v3 servers (BaGetter, Barn) actually parse; would need
  `_rels`/`[Content_Types].xml`/psmdcp to satisfy nuget.org's stricter
  reader, which isn't a goal here.
- **Polling**: 1s interval, 90s timeout, hardcoded constants — not
  exposed as flags. Kept the CLI surface to what the spec asked for
  (`--push`, `--package`); can add `--poll-interval`/`--poll-timeout`
  later if a real feed needs tuning. Originally 30s; raised after review
  flagged that a 30s oracle timeout equals Barn's own planned 30s index-
  regen debounce default — a coin-flip flake, not a margin. 90s gives
  2–3x headroom over that debounce window without the oracle silently
  encoding assumptions about the target's regen latency.
- **Registration fields treated as required**: only `catalogEntry.id` and
  `catalogEntry.version` (non-empty). Everything else NuGet v3 lists
  (`authors`, `published`, `tags`, `licenseUrl`, etc.) is optional/
  informational, consistent with the spec itself only marking
  `packageHash` as conditionally checked ("where present").
- **Machine-readable output**: reused the existing `-o/--output`
  flag instead of adding a separate `--json`, since every other command
  in the codebase already works that way.
- **Auth negative-check target**: `DELETE` on a guaranteed-nonexistent id
  with garbled credentials, not a garbage-credentialed push — zero
  side-effect risk regardless of server behavior.
- **`PackagePublish` responds-check in read-only mode**: skipped, not
  faked. It's a PUT-only endpoint; there's no safe way to probe it
  without `--push`.
- **Delete-support detection**: inferred from whether the package is
  still downloadable after `Delete()`, rather than issuing a second
  "hard delete" call — the codebase only exposes one delete endpoint, so
  a second call would be redundant.
- **Auth negative-check classification**: `401`/`403` → `pass`, `404` →
  `warn` (not `fail`), anything else → `fail`. The v3 spec doesn't mandate
  whether a server checks package existence or auth first; a
  existence-first server legitimately 404s with the garbled credentials
  never evaluated. Originally classified 404 as `fail`, which would have
  false-failed any spec-legal server that happens to order those checks
  that way — corrected after review.

## Known risk, not addressed by this iteration

The minimal `.nupkg`'s intentional non-OPC-compliance (see above) is fine
for testing *nugctl's own* round-trip logic, but must not become the de
facto definition of "a valid package" for Barn's parser once that exists.
Real client output (`dotnet pack`) is OPC-compliant — `[Content_Types].xml`,
`_rels`, the works. If Barn's `ParseUpload` is only ever exercised against
this tool's lenient bare-zip fixture, its acceptance criteria will quietly
drift looser than the spec, discovered only when a real client's package
fails against it in production. The fix belongs to Barn's test suite, not
this tool: harvest a handful of real `dotnet pack` output as golden
fixtures, test `ParseUpload` against those, and treat this tool's minimal
nupkg as one deliberately-lenient input case among several — not the only
one.
- **Cleanup-failure severity**: reports `warn`, not `fail`, when a feed
  doesn't support hard delete (confirmed acceptable for Barn's dev
  target).
