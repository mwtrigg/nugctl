# feed verify Implementation Plan

**Goal:** Add `nugctl feed verify`, a conformance-oracle command that checks a NuGet v3 feed (Barn, BaGetter) with read-only, negative, and optional push round-trip checks, exiting 0/1/2 for CI use.

**Architecture:** New `internal/verify` package (no cobra dependency, testable via `httptest`) does all the checking and returns a `*Report`. `cmd/feed_verify.go` wires flags, prints the report via the existing `internal/output` helpers, and calls `os.Exit(report.ExitCode())`. Two client methods (`FlatContainerVersions`, byte-oriented `PullBytes`/`PushBytes`) are added to `internal/client/nuget.go` to support it.

**Tech Stack:** Go stdlib only (`archive/zip`, `crypto/sha512`, `encoding/base64`, `net/http/httptest`) — no new dependencies.

**Full design:** `docs/superpowers/specs/2026-07-23-feed-verify-design.md` — read it for rationale; this plan is the "how," that doc is the "why."

## Global Constraints

- Follow existing cobra/output/profile conventions exactly — no restructuring of existing commands.
- No new CLI flags beyond `--push` and `--package` on `feed verify` (reuse global `--profile`/`--url`/`-o`/etc.).
- No network in tests — `httptest` only.
- `feed verify`'s `RunE` prints its own report and calls `os.Exit` directly (documented exception to the rest of the codebase's error-return convention).

---

### Task 1: Client — `FlatContainerVersions` and byte-oriented pull/push

**Files:**
- Modify: `internal/client/nuget.go`
- Test: `internal/client/nuget_test.go` (new file)

**Interfaces:**
- Produces: `FlatContainerVersions struct { Versions []string }`, `func (c *Client) FlatContainerVersions(id string) (*FlatContainerVersions, error)`, `func (c *Client) PullBytes(id, version string) ([]byte, error)`, `func (c *Client) PushBytes(filename string, data []byte) error`

- [ ] Add to `nuget.go` after the `--- Pull ---` section:

```go
// --- Flat container ---

type FlatContainerVersions struct {
	Versions []string `json:"versions"`
}

func (c *Client) FlatContainerVersions(id string) (*FlatContainerVersions, error) {
	var out FlatContainerVersions
	err := c.withResource("PackageBaseAddress", c.BaseURL+"/v3/package", func(base string) error {
		u := fmt.Sprintf("%s/%s/index.json", strings.TrimRight(base, "/"), strings.ToLower(id))
		return c.get(u, &out)
	})
	return &out, err
}
```

- [ ] Refactor `Pull` to extract byte-fetching into `PullBytes`, keeping `Pull`'s file-writing behavior identical:

```go
// PullBytes downloads a package version's .nupkg bytes without writing to disk.
func (c *Client) PullBytes(id, version string) ([]byte, error) {
	var data []byte
	err := c.withResource("PackageBaseAddress", c.BaseURL+"/v3/package", func(base string) error {
		dlURL := fmt.Sprintf("%s/%s/%s/%s.%s.nupkg",
			strings.TrimRight(base, "/"),
			strings.ToLower(id),
			strings.ToLower(version),
			strings.ToLower(id),
			strings.ToLower(version),
		)
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "GET %s\n", dlURL)
		}
		req, err := http.NewRequest(http.MethodGet, dlURL, nil)
		if err != nil {
			return err
		}
		c.setAuth(req)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError(dlURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return categorize(resp.StatusCode, string(body), dlURL)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		data = body
		return nil
	})
	return data, err
}

func (c *Client) Pull(id, version, outDir string) (string, error) {
	data, err := c.PullBytes(id, version)
	if err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(outDir, fmt.Sprintf("%s.%s.nupkg", id, version)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}
```

  Delete the old body of `Pull` (the `withResource` block that wrote directly to a file) — replace the whole `--- Pull ---` section with the two functions above.

- [ ] Refactor `Push` to extract multipart-upload into `PushBytes`, keeping `Push`'s file-reading behavior identical:

```go
// PushBytes uploads package bytes (already read into memory) to the feed.
// filename is used for the multipart form's filename field only.
func (c *Client) PushBytes(filename string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("package", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	w.Close()
	contentType := w.FormDataContentType()
	payload := buf.Bytes()

	return c.withResource("PackagePublish", c.BaseURL+"/api/v2/package", func(base string) error {
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "PUT %s\n", base)
		}
		req, err := http.NewRequest(http.MethodPut, base, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		c.setAuth(req)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError(base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return categorize(resp.StatusCode, string(body), base)
		}
		return nil
	})
}

func (c *Client) Push(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return c.PushBytes(filepath.Base(path), data)
}
```

  Replace the whole `--- Push ---` section with the two functions above.

- [ ] Write `internal/client/nuget_test.go`:

```go
package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFlatContainerVersions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		w.Write([]byte(`{"versions":["1.0.0","1.1.0"]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	got, err := c.FlatContainerVersions("MyPkg")
	if err != nil {
		t.Fatalf("FlatContainerVersions: %v", err)
	}
	if len(got.Versions) != 2 || got.Versions[0] != "1.0.0" {
		t.Fatalf("Versions = %v, want [1.0.0 1.1.0]", got.Versions)
	}
}

func TestPushBytesThenPullBytesRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var pushed []byte
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
	})
	mux.HandleFunc("/api/v2/package", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(10 << 20)
		f, _, err := r.FormFile("package")
		if err != nil {
			t.Fatalf("FormFile: %v", err)
		}
		defer f.Close()
		buf := make([]byte, 1024)
		n, _ := f.Read(buf)
		pushed = buf[:n]
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("/v3/package/mypkg/1.0.0/mypkg.1.0.0.nupkg", func(w http.ResponseWriter, r *http.Request) {
		w.Write(pushed)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	if err := c.PushBytes("MyPkg.1.0.0.nupkg", []byte("fake nupkg bytes")); err != nil {
		t.Fatalf("PushBytes: %v", err)
	}
	got, err := c.PullBytes("MyPkg", "1.0.0")
	if err != nil {
		t.Fatalf("PullBytes: %v", err)
	}
	if string(got) != "fake nupkg bytes" {
		t.Fatalf("PullBytes = %q, want %q", got, "fake nupkg bytes")
	}
}
```

- [ ] Run: `go build ./... && go test ./internal/client/...`
  Expected: PASS (all existing client tests plus the two new ones).

- [ ] Commit:

```bash
git add internal/client/nuget.go internal/client/nuget_test.go
git commit -m "Add FlatContainerVersions and byte-oriented Pull/Push to the client"
```

---

### Task 2: `internal/verify` — core types and orchestrator skeleton

**Files:**
- Create: `internal/verify/verify.go`
- Test: `internal/verify/verify_test.go`

**Interfaces:**
- Consumes: `*client.Client` (from Task 1's package, already exists)
- Produces: `type Status string` with `StatusPass/StatusFail/StatusWarn/StatusSkip`; `type Check struct { Name, Category string; Status Status; Detail string; Err string }`; `type Report struct { FeedURL string; Checks []Check; Aborted bool; AbortReason string }` with `func (r *Report) ExitCode() int` and `func (r *Report) Add(c Check)`; `type Options struct { Push bool; Package string }`; `func Run(c *client.Client, opts Options) *Report`

- [ ] Write `internal/verify/verify.go`:

```go
// Package verify implements nugctl's conformance-oracle checks for NuGet v3
// feed implementations (Barn, BaGetter): read-only structural checks,
// negative-path checks, and an optional push round-trip.
package verify

import "github.com/mwtrigg/nugctl/internal/client"

type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	StatusWarn Status = "warn"
	StatusSkip Status = "skip"
)

// Check is one assertion's outcome.
type Check struct {
	Name     string `json:"name"`
	Category string `json:"category"` // "readonly" | "negative" | "push"
	Status   Status `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Err      string `json:"err,omitempty"`
}

// Report is the full result of a verify run.
type Report struct {
	FeedURL     string  `json:"feedUrl"`
	Checks      []Check `json:"checks"`
	Aborted     bool    `json:"aborted"`
	AbortReason string  `json:"abortReason,omitempty"`
}

// Add appends a check to the report.
func (r *Report) Add(c Check) {
	r.Checks = append(r.Checks, c)
}

// ExitCode implements the CI exit contract: 2 on abort, 1 on any failed
// check, 0 otherwise. Warn/skip never affect the exit code.
func (r *Report) ExitCode() int {
	if r.Aborted {
		return 2
	}
	for _, c := range r.Checks {
		if c.Status == StatusFail {
			return 1
		}
	}
	return 0
}

// Counts returns the number of checks in each status, for summary lines.
func (r *Report) Counts() (pass, fail, warn, skip int) {
	for _, c := range r.Checks {
		switch c.Status {
		case StatusPass:
			pass++
		case StatusFail:
			fail++
		case StatusWarn:
			warn++
		case StatusSkip:
			skip++
		}
	}
	return
}

// Options controls which checks Run executes.
type Options struct {
	Push    bool   // also run the push round-trip (Task 6)
	Package string // target package for integrity checks; "" = auto-discover
}

// Run executes all applicable checks against c and returns the full report.
// It never returns an error — every failure mode is represented in the
// returned Report (including Report.Aborted for fatal setup failures).
func Run(c *client.Client, opts Options) *Report {
	r := &Report{FeedURL: c.BaseURL}
	runReadonly(c, opts, r)
	if r.Aborted {
		return r
	}
	runNegative(c, r)
	if opts.Push {
		runPush(c, r)
	}
	return r
}
```

- [ ] Write `internal/verify/verify_test.go`:

```go
package verify

import "testing"

func TestReport_ExitCode(t *testing.T) {
	cases := []struct {
		name string
		r    Report
		want int
	}{
		{"empty", Report{}, 0},
		{"all pass", Report{Checks: []Check{{Status: StatusPass}, {Status: StatusWarn}, {Status: StatusSkip}}}, 0},
		{"one fail", Report{Checks: []Check{{Status: StatusPass}, {Status: StatusFail}}}, 1},
		{"aborted wins over fail", Report{Aborted: true, Checks: []Check{{Status: StatusFail}}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.ExitCode(); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReport_Counts(t *testing.T) {
	r := Report{Checks: []Check{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusFail},
		{Status: StatusWarn}, {Status: StatusSkip},
	}}
	pass, fail, warn, skip := r.Counts()
	if pass != 2 || fail != 1 || warn != 1 || skip != 1 {
		t.Errorf("Counts() = %d,%d,%d,%d, want 2,1,1,1", pass, fail, warn, skip)
	}
}
```

  This test references `runReadonly`, `runNegative`, `runPush` only inside `Run` (not directly), so it compiles once Task 3/5/6 add those — for now, add minimal stub versions so Task 2 compiles standalone:

- [ ] Add stubs at the bottom of `verify.go` (Tasks 3, 5, 6 will replace these in their own files — delete the stub block when each task lands):

```go
func runReadonly(c *client.Client, opts Options, r *Report) {}
func runNegative(c *client.Client, r *Report)               {}
func runPush(c *client.Client, r *Report)                   {}
```

- [ ] Run: `go build ./... && go test ./internal/verify/...`
  Expected: PASS.

- [ ] Commit:

```bash
git add internal/verify/verify.go internal/verify/verify_test.go
git commit -m "Add internal/verify core types (Check, Report, ExitCode)"
```

---

### Task 3: Read-only checks — service index, resource presence, resource probes

**Files:**
- Create: `internal/verify/readonly.go`
- Modify: `internal/verify/verify.go` (remove the `runReadonly` stub — the real one now lives in `readonly.go`)
- Test: `internal/verify/readonly_test.go`

**Interfaces:**
- Consumes: `Report.Add`, `client.Client.ServiceIndex()`, `client.Client.Search()` (both already exist in `internal/client/nuget.go`)
- Produces: `func runReadonly(c *client.Client, opts Options, r *Report)`, `var requiredResourceTypes = []string{...}` (used again by Task 4/5 if needed)

- [ ] Remove the `func runReadonly(c *client.Client, opts Options, r *Report) {}` stub line from `verify.go`.

- [ ] Write `internal/verify/readonly.go`:

```go
package verify

import (
	"fmt"

	"github.com/mwtrigg/nugctl/internal/client"
)

// requiredResourceTypes are the @type prefixes every conformant v3 feed
// must advertise for nugctl's core operations to work.
var requiredResourceTypes = []string{
	"SearchQueryService",
	"RegistrationsBaseUrl",
	"PackageBaseAddress",
	"PackagePublish/2.0.0",
}

func runReadonly(c *client.Client, opts Options, r *Report) {
	idx, err := c.ServiceIndex()
	if err != nil {
		r.Aborted = true
		r.AbortReason = fmt.Sprintf("could not fetch service index: %v", err)
		return
	}

	present := map[string]bool{}
	for _, t := range requiredResourceTypes {
		found := hasResourceType(idx, t)
		present[t] = found
		status := StatusPass
		detail := "advertised in service index"
		if !found {
			status = StatusFail
			detail = "not advertised in service index"
		}
		r.Add(Check{Name: "resource present: " + t, Category: "readonly", Status: status, Detail: detail})
	}

	if present["SearchQueryService"] {
		if _, err := c.Search("", 0, 1, false); err != nil {
			r.Add(Check{Name: "resource responds: SearchQueryService", Category: "readonly", Status: StatusFail, Detail: "query failed", Err: err.Error()})
		} else {
			r.Add(Check{Name: "resource responds: SearchQueryService", Category: "readonly", Status: StatusPass})
		}
	}

	r.Add(Check{
		Name: "resource responds: PackagePublish/2.0.0", Category: "readonly",
		Status: StatusSkip,
		Detail: "PUT-only endpoint; no safe read-only probe. Run with --push to exercise it.",
	})

	runPackageIntegrity(c, opts, r)
}

func hasResourceType(idx *client.ServiceIndex, typePrefix string) bool {
	for _, res := range idx.Resources {
		if len(res.Type) >= len(typePrefix) && res.Type[:len(typePrefix)] == typePrefix {
			return true
		}
	}
	return false
}
```

  `runPackageIntegrity` is defined in Task 4 (`readonly.go` grows there — keep this task's build green by adding a temporary stub, removed in Task 4):

- [ ] Add a temporary stub at the bottom of `readonly.go` (Task 4 replaces it):

```go
func runPackageIntegrity(c *client.Client, opts Options, r *Report) {}
```

- [ ] Write `internal/verify/readonly_test.go`:

```go
package verify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mwtrigg/nugctl/internal/client"
)

// compliantIndexJSON advertises all four required resource types, pointed
// back at srv so probes stay local.
func compliantIndexJSON(base string) string {
	return `{"version":"3.0.0","resources":[
		{"@id":"` + base + `/search","@type":"SearchQueryService/3.4.0"},
		{"@id":"` + base + `/registration","@type":"RegistrationsBaseUrl/3.6.0"},
		{"@id":"` + base + `/flatcontainer","@type":"PackageBaseAddress/3.0.0"},
		{"@id":"` + base + `/publish","@type":"PackagePublish/2.0.0"}
	]}`
}

func TestRunReadonly_CompliantFeed_AllResourcesPass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(compliantIndexJSON(srv.URL)))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalHits":0,"data":[]}`))
	})

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runReadonly(c, Options{}, r)

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	for _, t2 := range requiredResourceTypes {
		found := false
		for _, chk := range r.Checks {
			if chk.Name == "resource present: "+t2 {
				found = true
				if chk.Status != StatusPass {
					t.Errorf("resource present: %s = %s, want pass", t2, chk.Status)
				}
			}
		}
		if !found {
			t.Errorf("no check emitted for resource type %s", t2)
		}
	}
}

func TestRunReadonly_MissingResource_Fails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Omits PackageBaseAddress entirely.
		w.Write([]byte(`{"version":"3.0.0","resources":[
			{"@id":"http://x/search","@type":"SearchQueryService/3.4.0"},
			{"@id":"http://x/registration","@type":"RegistrationsBaseUrl/3.6.0"},
			{"@id":"http://x/publish","@type":"PackagePublish/2.0.0"}
		]}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runReadonly(c, Options{}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "resource present: PackageBaseAddress" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("resource present: PackageBaseAddress = %s, want fail", got)
	}
}

func TestRunReadonly_UnreachableFeed_Aborts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	c := client.New("http://127.0.0.1:1", "", false, false, true) // nothing listens here
	r := &Report{}
	runReadonly(c, Options{}, r)

	if !r.Aborted {
		t.Fatal("expected Aborted = true for an unreachable feed")
	}
	if len(r.Checks) != 0 {
		t.Errorf("expected no checks recorded on abort, got %d", len(r.Checks))
	}
}
```

- [ ] Run: `go test ./internal/verify/... -run TestRunReadonly -v`
  Expected: PASS (3 tests).

- [ ] Commit:

```bash
git add internal/verify/readonly.go internal/verify/readonly_test.go internal/verify/verify.go
git commit -m "Add read-only resource-presence and probe checks"
```

---

### Task 4: Package integrity checks — registration, flat-container, hash

**Files:**
- Modify: `internal/verify/readonly.go` (replace the `runPackageIntegrity` stub)
- Modify: `internal/client/nuget.go:262-279` — no change needed, `CatalogEntry.PackageHash` already exists
- Test: `internal/verify/readonly_test.go` (append)

**Interfaces:**
- Consumes: `client.Client.Registration(id)`, `client.Client.Search(...)`, `client.Client.FlatContainerVersions(id)` (Task 1), `client.Client.PullBytes(id, version)` (Task 1)
- Produces: `func runPackageIntegrity(c *client.Client, opts Options, r *Report)`

- [ ] Replace the stub `func runPackageIntegrity(c *client.Client, opts Options, r *Report) {}` in `readonly.go` with:

```go
func runPackageIntegrity(c *client.Client, opts Options, r *Report) {
	const cat = "readonly"
	id := opts.Package
	if id == "" {
		res, err := c.Search("", 0, 1, false)
		if err != nil || len(res.Data) == 0 {
			skip := "no package available to test (feed appears empty; pass --package to specify one)"
			r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusSkip, Detail: skip})
			r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusSkip, Detail: skip})
			r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: skip})
			return
		}
		id = res.Data[0].ID
	}

	idx, err := c.Registration(id)
	if err != nil {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusFail, Detail: "fetching registration for " + id, Err: err.Error()})
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusSkip, Detail: "registration fetch failed"})
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: "registration fetch failed"})
		return
	}

	var regVersions []string
	malformed := idx.Count == 0 || len(idx.Items) == 0
	for _, page := range idx.Items {
		for _, leaf := range page.Items {
			if leaf.CatalogEntry.ID == "" || leaf.CatalogEntry.Version == "" {
				malformed = true
				continue
			}
			regVersions = append(regVersions, leaf.CatalogEntry.Version)
		}
	}
	if malformed || len(regVersions) == 0 {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("id=%s: missing pages/items or leaves without id/version", id)})
	} else {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s, %d version(s)", id, len(regVersions))})
	}

	flat, err := c.FlatContainerVersions(id)
	if err != nil {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusFail, Detail: "fetching flat-container list", Err: err.Error()})
	} else if !sameVersionSet(regVersions, flat.Versions) {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("registration=%v flat-container=%v", regVersions, flat.Versions)})
	} else {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusPass})
	}

	runHashCheck(c, id, idx, r)
}

// sameVersionSet compares two version lists case-insensitively, ignoring order.
func sameVersionSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(vs []string) map[string]bool {
		m := make(map[string]bool, len(vs))
		for _, v := range vs {
			m[strings.ToLower(v)] = true
		}
		return m
	}
	am, bm := norm(a), norm(b)
	for v := range am {
		if !bm[v] {
			return false
		}
	}
	return true
}

func runHashCheck(c *client.Client, id string, idx *client.RegistrationIndex, r *Report) {
	const cat = "readonly"
	var target *client.CatalogEntry
	var version string
	for _, page := range idx.Items {
		for i := range page.Items {
			e := &page.Items[i].CatalogEntry
			if e.PackageHash != "" {
				target = e
				version = e.Version
				break
			}
		}
	}
	if target == nil {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: "no version in the registration advertises packageHash"})
		return
	}

	data, err := c.PullBytes(id, version)
	if err != nil {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("downloading %s %s", id, version), Err: err.Error()})
		return
	}
	sum := sha512.Sum512(data)
	got := base64.StdEncoding.EncodeToString(sum[:])
	if got != target.PackageHash {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("id=%s version=%s registration=%s computed=%s", id, version, target.PackageHash, got)})
		return
	}
	r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s version=%s", id, version)})
}
```

- [ ] Update the `import` block at the top of `readonly.go` to:

```go
import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
)
```

- [ ] Append to `internal/verify/readonly_test.go`:

```go
func registrationJSON(id, version, hash string) string {
	return `{"count":1,"items":[{"count":1,"items":[
		{"catalogEntry":{"id":"` + id + `","version":"` + version + `","packageHash":"` + hash + `","packageHashAlgorithm":"SHA512"}}
	]}]}`
}

func TestRunPackageIntegrity_HashMismatch_Fails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(compliantIndexJSON(srv.URL)))
	})
	mux.HandleFunc("/registration/mypkg/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(registrationJSON("MyPkg", "1.0.0", "not-the-real-hash")))
	})
	mux.HandleFunc("/flatcontainer/mypkg/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"versions":["1.0.0"]}`))
	})
	mux.HandleFunc("/flatcontainer/mypkg/1.0.0/mypkg.1.0.0.nupkg", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("actual package bytes"))
	})

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runPackageIntegrity(c, Options{Package: "MyPkg"}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "package hash matches registration" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("package hash matches registration = %s, want fail", got)
	}
}

func TestRunPackageIntegrity_EmptyFeedNoPackageFlag_Skips(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(compliantIndexJSON(srv.URL)))
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"totalHits":0,"data":[]}`))
	})

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runPackageIntegrity(c, Options{}, r)

	for _, chk := range r.Checks {
		if chk.Status != StatusSkip {
			t.Errorf("check %q = %s, want skip on an empty feed with no --package", chk.Name, chk.Status)
		}
	}
}
```

- [ ] Run: `go test ./internal/verify/... -run TestRunPackageIntegrity -v`
  Expected: PASS (2 tests).

- [ ] Run full package: `go build ./... && go test ./internal/verify/... ./internal/client/...`
  Expected: PASS.

- [ ] Commit:

```bash
git add internal/verify/readonly.go internal/verify/readonly_test.go
git commit -m "Add registration/flat-container/hash integrity checks"
```

---

### Task 5: Negative checks — 404 shape and garbled-credentials auth rejection

**Files:**
- Create: `internal/verify/negative.go`
- Modify: `internal/verify/verify.go` (remove the `runNegative` stub)
- Test: `internal/verify/negative_test.go`

**Interfaces:**
- Consumes: `client.Client.Registration(id)`, `client.IsNotFound(err)`, `client.IsUnauthorized(err)` (all exist in `internal/client`); `client.Client.Delete(id, version)` (exists)
- Produces: `func runNegative(c *client.Client, r *Report)`

- [ ] Remove the `func runNegative(c *client.Client, r *Report) {}` stub from `verify.go`.

- [ ] Write `internal/verify/negative.go`:

```go
package verify

import (
	"fmt"
	"math/rand"

	"github.com/mwtrigg/nugctl/internal/client"
)

func runNegative(c *client.Client, r *Report) {
	const cat = "negative"
	nonexistentID := fmt.Sprintf("nugctl-verify-does-not-exist-%d", rand.Int63())

	_, err := c.Registration(nonexistentID)
	switch {
	case err == nil:
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusFail, Detail: "expected an error, got HTTP 200"})
	case client.IsNotFound(err):
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusPass})
	default:
		r.Add(Check{Name: "404 shape for nonexistent package", Category: cat, Status: StatusFail, Detail: "expected a 404, got a different error", Err: err.Error()})
	}

	if c.APIKey == "" && c.BasicAuthUser == "" {
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusSkip, Detail: "profile has no credentials configured"})
		return
	}

	bad := client.New(c.BaseURL, "definitely-not-a-real-api-key", c.Verbose, c.Insecure, true)
	if c.BasicAuthUser != "" {
		bad.BasicAuthUser = c.BasicAuthUser
		bad.BasicAuthPass = "definitely-not-the-real-password"
	}
	delErr := bad.Delete(nonexistentID, "1.0.0")
	switch {
	case delErr == nil:
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusFail, Detail: "expected an auth error, got HTTP 2xx"})
	case client.IsUnauthorized(delErr):
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusPass})
	default:
		r.Add(Check{Name: "401/403 for garbled credentials", Category: cat, Status: StatusFail, Detail: "expected 401/403, got a different error (server may leak existence before enforcing auth)", Err: delErr.Error()})
	}
}
```

- [ ] Write `internal/verify/negative_test.go`:

```go
package verify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mwtrigg/nugctl/internal/client"
)

func TestRunNegative_WellFormed404_Passes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "404 shape for nonexistent package" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("404 shape check = %s, want pass", got)
	}
}

func TestRunNegative_MalformedEmptyOK_Fails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		w.Write([]byte(`{"count":0,"items":[]}`)) // 200 instead of 404
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "404 shape for nonexistent package" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("404 shape check = %s, want fail for a 200 response", got)
	}
}

func TestRunNegative_NoCredentials_SkipsAuthCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true) // no API key, no basic auth
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "401/403 for garbled credentials" {
			got = chk.Status
		}
	}
	if got != StatusSkip {
		t.Errorf("auth check = %s, want skip when no credentials are configured", got)
	}
}

func TestRunNegative_GarbledCredentials_ExpectsAuthRejection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		if r.Method == http.MethodDelete {
			if r.Header.Get("X-NuGet-ApiKey") != "the-real-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "the-real-key", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "401/403 for garbled credentials" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("auth check = %s, want pass (server correctly rejected garbled credentials)", got)
	}
}
```

- [ ] Run: `go test ./internal/verify/... -run TestRunNegative -v`
  Expected: PASS (4 tests).

- [ ] Commit:

```bash
git add internal/verify/negative.go internal/verify/negative_test.go internal/verify/verify.go
git commit -m "Add negative checks: 404 shape and garbled-credentials auth rejection"
```

---

### Task 6: Minimal nupkg builder

**Files:**
- Create: `internal/verify/nupkg.go`
- Test: `internal/verify/nupkg_test.go`

**Interfaces:**
- Produces: `func buildMinimalNupkg(id, version string) ([]byte, error)`

- [ ] Write `internal/verify/nupkg.go`:

```go
package verify

import (
	"archive/zip"
	"bytes"
	"fmt"
)

// buildMinimalNupkg builds a minimal but valid .nupkg in memory: a bare zip
// containing {id}.nuspec at the root plus one content file. This is
// deliberately not full OPC-compliant (no _rels, [Content_Types].xml, or
// core-properties psmdcp) — that's what self-hosted v3 servers like
// BaGetter and Barn actually parse. nuget.org's stricter reader would need
// more; that's out of scope for this conformance tool.
func buildMinimalNupkg(id, version string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	nuspec := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd">
  <metadata>
    <id>%s</id>
    <version>%s</version>
    <authors>nugctl</authors>
    <description>Transient package generated by nugctl feed verify --push.</description>
    <files>
      <file src="readme.txt" target="readme.txt" />
    </files>
  </metadata>
</package>`, id, version)

	nuspecW, err := zw.Create(id + ".nuspec")
	if err != nil {
		return nil, err
	}
	if _, err := nuspecW.Write([]byte(nuspec)); err != nil {
		return nil, err
	}

	readmeW, err := zw.Create("readme.txt")
	if err != nil {
		return nil, err
	}
	if _, err := readmeW.Write([]byte("generated by nugctl feed verify --push\n")); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
```

- [ ] Write `internal/verify/nupkg_test.go`:

```go
package verify

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestBuildMinimalNupkg_ValidZipWithNuspecAndContentFile(t *testing.T) {
	data, err := buildMinimalNupkg("nugctl-verify-123", "1.0.0")
	if err != nil {
		t.Fatalf("buildMinimalNupkg: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("resulting bytes are not a valid zip: %v", err)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["nugctl-verify-123.nuspec"] {
		t.Errorf("expected nuspec at root, got entries %v", names)
	}
	if !names["readme.txt"] {
		t.Errorf("expected a content file, got entries %v", names)
	}
}
```

- [ ] Run: `go test ./internal/verify/... -run TestBuildMinimalNupkg -v`
  Expected: PASS.

- [ ] Commit:

```bash
git add internal/verify/nupkg.go internal/verify/nupkg_test.go
git commit -m "Add minimal in-memory .nupkg builder for the push round-trip"
```

---

### Task 7: Push round-trip checks

**Files:**
- Create: `internal/verify/push.go`
- Modify: `internal/verify/verify.go` (remove the `runPush` stub)
- Test: `internal/verify/push_test.go`

**Interfaces:**
- Consumes: `buildMinimalNupkg(id, version)` (Task 6), `client.Client.PushBytes`, `client.Client.PullBytes`, `client.Client.Search`, `client.Client.Registration`, `client.Client.Delete` (all exist)
- Produces: `func runPush(c *client.Client, r *Report)`

- [ ] Remove the `func runPush(c *client.Client, r *Report) {}` stub from `verify.go`.

- [ ] Write `internal/verify/push.go`:

```go
package verify

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/mwtrigg/nugctl/internal/client"
)

const (
	pollInterval = 1 * time.Second
	pollTimeout  = 30 * time.Second
)

func runPush(c *client.Client, r *Report) {
	const cat = "push"
	id := fmt.Sprintf("nugctl-verify-%d", time.Now().Unix())
	version := "1.0.0"

	data, err := buildMinimalNupkg(id, version)
	if err != nil {
		r.Add(Check{Name: "build minimal package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "build minimal package", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s version=%s", id, version)})

	if err := c.PushBytes(fmt.Sprintf("%s.%s.nupkg", id, version), data); err != nil {
		r.Add(Check{Name: "push package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "push package", Category: cat, Status: StatusPass})

	if !pollUntil(func() bool { return packageVisible(c, id, version) }) {
		r.Add(Check{Name: "package appears in search/registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("not visible after %s", pollTimeout)})
		return
	}
	r.Add(Check{Name: "package appears in search/registration", Category: cat, Status: StatusPass})

	downloaded, err := c.PullBytes(id, version)
	if err != nil {
		r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusFail, Err: err.Error()})
	} else {
		want := sha512.Sum512(data)
		got := sha512.Sum512(downloaded)
		if base64.StdEncoding.EncodeToString(want[:]) != base64.StdEncoding.EncodeToString(got[:]) {
			r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusFail, Detail: "SHA-512 of downloaded bytes does not match what was pushed"})
		} else {
			r.Add(Check{Name: "download matches pushed hash", Category: cat, Status: StatusPass})
		}
	}

	if err := c.Delete(id, version); err != nil {
		r.Add(Check{Name: "unlist/delete package", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	r.Add(Check{Name: "unlist/delete package", Category: cat, Status: StatusPass})

	if !pollUntil(func() bool { return !packageInSearch(c, id) }) {
		r.Add(Check{Name: "package disappears from default search", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("still visible after %s", pollTimeout)})
	} else {
		r.Add(Check{Name: "package disappears from default search", Category: cat, Status: StatusPass})
	}

	_, err = c.PullBytes(id, version)
	switch {
	case err == nil:
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusWarn, Detail: "package still downloadable by exact version after delete — feed unlists rather than hard-deletes; test package left behind"})
	case client.IsNotFound(err):
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusPass, Detail: "package no longer downloadable — feed hard-deletes"})
	default:
		r.Add(Check{Name: "feed delete support", Category: cat, Status: StatusFail, Detail: "unexpected error re-downloading after delete", Err: err.Error()})
	}
}

func packageVisible(c *client.Client, id, version string) bool {
	if !packageInSearch(c, id) {
		return false
	}
	entry, err := c.RegistrationVersion(id, version)
	return err == nil && entry != nil && entry.Version == version
}

func packageInSearch(c *client.Client, id string) bool {
	res, err := c.Search(id, 0, 20, true)
	if err != nil {
		return false
	}
	for _, p := range res.Data {
		if p.ID == id {
			return true
		}
	}
	return false
}

// pollUntil polls cond at pollInterval until it returns true or pollTimeout
// elapses. Returns whether cond became true in time.
func pollUntil(cond func() bool) bool {
	deadline := time.Now().Add(pollTimeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}
```

- [ ] Write `internal/verify/push_test.go` — a small stateful fake feed covering push/search/registration/download/delete:

```go
package verify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwtrigg/nugctl/internal/client"
)

// fakeFeed is a minimal stateful in-memory NuGet v3 feed used to exercise
// the push round-trip without any real network or server implementation.
type fakeFeed struct {
	mu       sync.Mutex
	packages map[string][]byte // "id/version" -> nupkg bytes
	unlisted map[string]bool
	hardDelete bool // if true, Delete removes the package entirely
}

func newFakeFeed(hardDelete bool) *fakeFeed {
	return &fakeFeed{packages: map[string][]byte{}, unlisted: map[string]bool{}, hardDelete: hardDelete}
}

func (f *fakeFeed) server() *httptest.Server {
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		base := srv.URL
		fmt.Fprintf(w, `{"version":"3.0.0","resources":[
			{"@id":"%s/search","@type":"SearchQueryService/3.4.0"},
			{"@id":"%s/registration/","@type":"RegistrationsBaseUrl/3.6.0"},
			{"@id":"%s/flatcontainer/","@type":"PackageBaseAddress/3.0.0"},
			{"@id":"%s/publish","@type":"PackagePublish/2.0.0"}
		]}`, base, base, base, base)
	})

	mux.HandleFunc("/publish", func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(10 << 20)
		file, header, err := r.FormFile("package")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()
		buf := make([]byte, 1<<20)
		n, _ := file.Read(buf)
		id, version := parseNupkgFilename(header.Filename)
		f.mu.Lock()
		f.packages[id+"/"+version] = buf[:n]
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		f.mu.Lock()
		defer f.mu.Unlock()
		var data []map[string]any
		for key := range f.packages {
			id := strings.SplitN(key, "/", 2)[0]
			if f.unlisted[key] {
				continue
			}
			if q == "" || id == q {
				data = append(data, map[string]any{"id": id, "version": strings.SplitN(key, "/", 2)[1]})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"totalHits": len(data), "data": data})
	})

	mux.HandleFunc("/registration/", func(w http.ResponseWriter, r *http.Request) {
		// /registration/{id}/{version}.json
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/registration/"), "/")
		id := parts[0]
		version := strings.TrimSuffix(parts[1], ".json")
		f.mu.Lock()
		_, ok := f.packages[id+"/"+version]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"catalogEntry": map[string]any{"id": id, "version": version},
		})
	})

	mux.HandleFunc("/flatcontainer/", func(w http.ResponseWriter, r *http.Request) {
		// /flatcontainer/{id}/{version}/{id}.{version}.nupkg
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/flatcontainer/"), "/")
		id, version := parts[0], parts[1]
		key := id + "/" + version
		f.mu.Lock()
		data, ok := f.packages[key]
		f.mu.Unlock()
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Write(data)
	})

	mux.HandleFunc("/publish/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/publish/"), "/")
		id, version := parts[0], parts[1]
		key := id + "/" + version
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.hardDelete {
			delete(f.packages, key)
		} else {
			f.unlisted[key] = true
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv = httptest.NewServer(mux)
	return srv
}

func parseNupkgFilename(name string) (id, version string) {
	name = strings.TrimSuffix(name, ".nupkg")
	i := strings.LastIndex(name, ".")
	// version starts at the first digit-led segment; our test IDs never
	// contain a numeric dot-segment, so splitting at the last "N.N.N" works
	// via a simple heuristic: find the substring matching \d+\.\d+\.\d+ at the end.
	// For our fixed test id "nugctl-verify-<ts>" and version "1.0.0" the
	// filename is "nugctl-verify-<ts>.1.0.0.nupkg", so splitting on the
	// first "." that starts a numeric run gives the right answer.
	for j := 0; j < len(name); j++ {
		if name[j] == '.' && j+1 < len(name) && name[j+1] >= '0' && name[j+1] <= '9' {
			return name[:j], name[j+1:]
		}
	}
	return name, ""
	_ = i
}

func TestRunPush_FullRoundTrip_UnlistOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false) // unlist-only, like BaGetter's default
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, r)

	statuses := map[string]Status{}
	for _, chk := range r.Checks {
		statuses[chk.Name] = chk.Status
	}
	want := map[string]Status{
		"build minimal package":                      StatusPass,
		"push package":                                StatusPass,
		"package appears in search/registration":      StatusPass,
		"download matches pushed hash":                StatusPass,
		"unlist/delete package":                       StatusPass,
		"package disappears from default search":      StatusPass,
		"feed delete support":                         StatusWarn,
	}
	for name, wantStatus := range want {
		if statuses[name] != wantStatus {
			t.Errorf("check %q = %s, want %s", name, statuses[name], wantStatus)
		}
	}
}

func TestRunPush_FullRoundTrip_HardDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(true)
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "feed delete support" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("feed delete support = %s, want pass for a hard-delete feed", got)
	}
}

func TestPollUntil_TimesOutWithoutHanging(t *testing.T) {
	start := time.Now()
	got := pollUntil(func() bool { return false })
	if got {
		t.Fatal("expected pollUntil to return false when cond never succeeds")
	}
	if time.Since(start) < pollTimeout {
		t.Fatalf("pollUntil returned before the timeout elapsed: %s", time.Since(start))
	}
}
```

  Note: `TestPollUntil_TimesOutWithoutHanging` takes ~30s (the real `pollTimeout`). That's acceptable for this package's test suite but worth knowing when running `go test ./...` broadly. If it's too slow in practice, skip it with `t.Skip` under `-short` — add `if testing.Short() { t.Skip(...) }` as the first line if you hit CI time pressure; not required for this plan to land.

- [ ] Run: `go test ./internal/verify/... -run TestRunPush -v`
  Expected: PASS (2 tests, each takes a couple seconds for polling to resolve).

- [ ] Run full suite: `go build ./... && go test ./...`
  Expected: PASS (the `TestPollUntil_TimesOutWithoutHanging` test will take ~30s).

- [ ] Commit:

```bash
git add internal/verify/push.go internal/verify/push_test.go internal/verify/verify.go
git commit -m "Add push round-trip checks (push, poll, download, unlist, delete-support)"
```

---

### Task 8: `nugctl feed verify` command

**Files:**
- Create: `cmd/feed_verify.go`
- Modify: `cmd/feed.go:73` — add `feedCmd.AddCommand(feedVerifyCmd)` inside the existing `init()`

**Interfaces:**
- Consumes: `resolveClient()` (`cmd/root.go`), `effectiveFormat()` (`cmd/root.go`), `output.PrintJSON/PrintYAML/PrintTable` (`internal/output`), `verify.Run(c, verify.Options{...})`, `verify.Report`, `verify.Check`, `verify.Status*` (Task 2-7)

- [ ] Write `cmd/feed_verify.go`:

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/mwtrigg/nugctl/internal/output"
	"github.com/mwtrigg/nugctl/internal/verify"
	"github.com/spf13/cobra"
)

var feedVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Validate a feed's NuGet v3 conformance",
	Long: `Run read-only, negative, and (with --push) round-trip checks against a
feed to validate it implements the NuGet v3 protocol correctly. Designed to
run in CI: exit 0 means every executed check passed, exit 1 means at least
one check failed, exit 2 means verify couldn't even run (e.g. the service
index was unreachable).`,
	Example: `  nugctl feed verify
  nugctl feed verify --package Newtonsoft.Json
  nugctl feed verify --push -o json`,
	RunE: func(cmd *cobra.Command, args []string) error {
		push, _ := cmd.Flags().GetBool("push")
		pkg, _ := cmd.Flags().GetString("package")

		c, err := resolveClient()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}

		report := verify.Run(c, verify.Options{Push: push, Package: pkg})
		printVerifyReport(report)
		os.Exit(report.ExitCode())
		return nil
	},
}

func printVerifyReport(r *verify.Report) {
	switch effectiveFormat() {
	case output.FormatJSON:
		output.PrintJSON(r)
		return
	case output.FormatYAML:
		output.PrintYAML(r)
		return
	}

	if r.Aborted {
		fmt.Printf("ABORTED: %s\n\n", r.AbortReason)
	}
	pass, fail, warn, skip := r.Counts()
	fmt.Printf("Feed: %s\n", r.FeedURL)
	fmt.Printf("%d passed, %d failed, %d warned, %d skipped\n\n", pass, fail, warn, skip)

	headers := []string{"CATEGORY", "CHECK", "STATUS", "DETAIL"}
	rows := make([][]string, 0, len(r.Checks))
	for _, chk := range r.Checks {
		detail := chk.Detail
		if chk.Err != "" {
			if detail != "" {
				detail += ": "
			}
			detail += chk.Err
		}
		rows = append(rows, []string{chk.Category, chk.Name, string(chk.Status), detail})
	}
	output.PrintTable(headers, rows)
}

func init() {
	feedVerifyCmd.Flags().Bool("push", false, "also run round-trip checks (push, poll, download, unlist) against a writable feed")
	feedVerifyCmd.Flags().String("package", "", "package ID to use for integrity checks (default: first hit of an unfiltered search)")
	feedCmd.AddCommand(feedVerifyCmd)
}
```

- [ ] Run: `go build ./...`
  Expected: builds cleanly.

- [ ] Manual smoke test against a mock server, or a real BaGetter instance if one is running locally. If nothing's available, verify wiring with `--help`:

```bash
go run . feed verify --help
```
  Expected: shows `--push` and `--package` flags, plus the inherited global flags (`--profile`, `--url`, `-o`, etc.).

- [ ] If a local feed is available, run it for real and confirm the exit code:

```bash
go run . feed verify --url http://localhost:5000/v3/index.json; echo "exit=$?"
go run . feed verify --url http://localhost:5000/v3/index.json -o json | head -30
```

- [ ] Commit:

```bash
git add cmd/feed_verify.go cmd/feed.go
git commit -m "Add nugctl feed verify command"
```

---

### Task 9: Final verification pass

**Files:** none (verification only)

- [ ] Run the full build and test suite:

```bash
go build ./...
go vet ./...
go test ./...
```
  Expected: all green. (`internal/verify`'s push tests take a few seconds each due to real polling; `TestPollUntil_TimesOutWithoutHanging` takes ~30s — this is expected, not a hang.)

- [ ] Confirm `nugctl feed verify` is discoverable:

```bash
go run . feed --help
go run . feed verify --help
```
  Expected: `verify` listed under `feed`'s subcommands with its short description.

- [ ] Re-read `docs/superpowers/specs/2026-07-23-feed-verify-design.md` end to end and confirm every numbered requirement (1. read-only checks, 2. round-trip checks, 3. negative checks, 4. output/exit codes, 5. tests) has corresponding code. This is a manual cross-check, not a new task — if something's missing, add a follow-up task before considering this plan done.

- [ ] No commit for this task — it's a verification gate, not a change.
