# feed verify: known coverage gaps

`nugctl feed verify` is used as a NuGet v3 conformance oracle, so anything it
does not check is something a half-implemented feed can get wrong and still
pass. These are the gaps found so far (from testing it against Barn, a NuGet
v3 feed that was being brought up to the spec). Each is written as an issue:
what is unchecked, and a suggested check. Spec references are to
learn.microsoft.com/nuget/api.

Fixed already: standalone registration leaf `catalogEntry` as a URL string
(both shapes are accepted), and relist (`feed verify --push` now relists on
feeds that unlist on DELETE).

## TODO

None open at the moment.

## Done

- [x] **Service index type strings** (#8). `client type string: <resource>`
  warns when none of a resource's advertised `@type` values is one NuGet's
  client looks up by exact match (list from NuGet.Client `ServiceTypes.cs`).
  Note `SearchQueryService/3.0.0-rc` is a documented alias but is *not* on
  that list.
- [x] **Search `versions[]`, `prerelease`, `semVerLevel`, autocomplete** (#5).
  Readonly: `search result shape (versions[])`, `autocomplete: package ids`,
  `autocomplete: package versions`. With `--push`: a prerelease-only and a
  SemVer 2.0.0-only package, and `search excludes prerelease by default`,
  `search excludes SemVer 2.0.0 without semVerLevel`, plus the same two for
  autocomplete when it is advertised. nugctl's own searches now send
  `semVerLevel=2.0.0`, as NuGet's client does.
- [x] **Flat container `.nuspec` and version normalization** (#6). Readonly:
  `flat container .nuspec download`. With `--push`: a `1.00.0.0` package and
  `version normalized: flat container` / `: registration`,
  `download via normalized version`, `nuspec download matches pushed
  package` (re-serialized manifest warns), `duplicate push of an equivalent
  version` (409 passes; an accepted replace that leaves one version warns),
  and `unlisted version stays in flat container` on feeds that unlist.
- [x] **Push field name and relist of an absent version** (#7). With
  `--push`: `push accepts any multipart field name` (the prerelease package
  goes up as form field `nupkg`; a rejection warns, because NuGet's client
  always sends `package`, and the package is re-pushed) and
  `relist of an absent version gives 404` (405/501, no relist at all, warns).
- [x] **Registration bodies** (#4). Readonly: `registration required fields`
  (index `count`/`items`; page `@id`, `count`, `lower`, `upper`, plus
  `parent` and `items` on pages fetched out of line; leaf `@id`,
  `packageContent`, object `catalogEntry` with `@id`, `id`, `version`;
  out-of-line pages are fetched and their leaves feed the other checks) and
  `registration gzip: RegistrationsBaseUrl/3.4.0` / `3.6.0` (uncompressed
  warns). With `--push`: a mixed-case id with one dependency, and
  `original id casing: registration` / `: search`,
  `registration dependencies` (missing fails; absent `range` or
  `registration` warns; a different range fails) and, on feeds that unlist,
  `unlisted version stays in registration (listed:false)`.
- [x] **Proxy / read-only feeds** (#9). README, "Verifying a read-only or
  proxy feed": run without `--push`, with `--package <id>`.
