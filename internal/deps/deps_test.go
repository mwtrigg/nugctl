package deps

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

// pkg describes one registration leaf the fake feed should serve.
type pkg struct {
	id       string
	version  string
	unlisted bool
	deps     []depGroup // nil if this version declares no dependencies
}

type depGroup struct {
	targetFramework string
	deps            []dep
}

type dep struct {
	id  string
	rng string
}

// regFeedConfig extends fakeRegFeed with the edge cases the review flagged:
// a registration index page left out-of-line (only "@id"/"count", no
// "items" — a standard NuGet v3 representation for large pages) and a
// registration endpoint that fails outright instead of serving content.
type regFeedConfig struct {
	packages   []pkg
	outOfLine  map[string]bool // id (lowercased) -> serve its registration index with the page left out-of-line
	failStatus map[string]int  // id (lowercased) -> HTTP status the registration endpoint returns instead of content, every time

	// flakyStatus/flakyFailCount simulate a feed's rate limiter: for id
	// (lowercased), the registration endpoint returns flakyStatus[id] for
	// the first flakyFailCount[id] requests, then serves normally.
	flakyStatus    map[string]int
	flakyFailCount map[string]int
	retryAfter     map[string]string // id (lowercased) -> Retry-After header sent alongside a flaky failure
}

// feedProbe records what fakeRegFeedWithConfig's server actually received,
// for tests asserting on pacing and retry counts.
type feedProbe struct {
	mu             sync.Mutex
	attempts       map[string]int // id (lowercased) -> number of /registration/ requests received so far
	registrationAt []time.Time    // request time of every /registration/ call, in order
}

func (p *feedProbe) record(id string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts[id]++
	p.registrationAt = append(p.registrationAt, time.Now())
	return p.attempts[id]
}

// fakeRegFeed serves only the two v3 resources deps.Run needs: search (for
// full-feed discovery) and registration (for dependencyGroups).
func fakeRegFeed(t *testing.T, packages []pkg) *httptest.Server {
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{packages: packages})
	return srv
}

func fakeRegFeedWithConfig(t *testing.T, cfg regFeedConfig) (*httptest.Server, *feedProbe) {
	t.Helper()
	probe := &feedProbe{attempts: map[string]int{}}
	byID := map[string][]pkg{}
	var order []string
	for _, p := range cfg.packages {
		key := strings.ToLower(p.id)
		if _, ok := byID[key]; !ok {
			order = append(order, p.id)
		}
		byID[key] = append(byID[key], p)
	}

	pageItems := func(versions []pkg) []map[string]any {
		var items []map[string]any
		for _, v := range versions {
			var groups []map[string]any
			for _, g := range v.deps {
				var ds []map[string]any
				for _, d := range g.deps {
					ds = append(ds, map[string]any{"id": d.id, "range": d.rng})
				}
				group := map[string]any{"dependencies": ds}
				if g.targetFramework != "" {
					group["targetFramework"] = g.targetFramework
				}
				groups = append(groups, group)
			}
			entry := map[string]any{"id": v.id, "version": v.version, "listed": !v.unlisted}
			if groups != nil {
				entry["dependencyGroups"] = groups
			}
			items = append(items, map[string]any{"catalogEntry": entry})
		}
		return items
	}

	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		base := srv.URL
		fmt.Fprintf(w, `{"version":"3.0.0","resources":[
			{"@id":"%s/search","@type":"SearchQueryService/3.4.0"},
			{"@id":"%s/registration/","@type":"RegistrationsBaseUrl/3.6.0"}
		]}`, base, base)
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		var data []map[string]any
		for _, id := range order {
			data = append(data, map[string]any{"id": id, "version": byID[strings.ToLower(id)][0].version})
		}
		json.NewEncoder(w).Encode(map[string]any{"totalHits": len(data), "data": data})
	})

	mux.HandleFunc("/registration-page/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/registration-page/"), ".json")
		items := pageItems(byID[strings.ToLower(id)])
		json.NewEncoder(w).Encode(map[string]any{"count": len(items), "items": items})
	})

	mux.HandleFunc("/registration/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/registration/"), "/index.json")
		key := strings.ToLower(id)
		attempt := probe.record(key)
		if n, ok := cfg.flakyFailCount[key]; ok && attempt <= n {
			if ra, ok := cfg.retryAfter[key]; ok {
				w.Header().Set("Retry-After", ra)
			}
			http.Error(w, "simulated transient failure", cfg.flakyStatus[key])
			return
		}
		if status, ok := cfg.failStatus[key]; ok {
			http.Error(w, "simulated failure", status)
			return
		}
		versions, ok := byID[key]
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		items := pageItems(versions)
		if cfg.outOfLine[key] {
			json.NewEncoder(w).Encode(map[string]any{
				"count": 1,
				"items": []map[string]any{{"@id": srv.URL + "/registration-page/" + id + ".json", "count": len(items)}},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"count": 1,
			"items": []map[string]any{{"count": len(items), "items": items}},
		})
	})

	srv = httptest.NewServer(mux)
	return srv, probe
}

func TestRun_ClassifiesOKMissingUnsatisfied(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{
			targetFramework: "net8.0",
			deps: []dep{
				{id: "Newtonsoft.Json", rng: "13.0.0"},     // OK: 13.0.3 satisfies >=13.0.0
				{id: "DoesNotExist.Package", rng: "1.0.0"}, // MISSING: no such package in feed
				{id: "OldLib", rng: "[2.0.0,3.0.0)"},       // UNSATISFIED: only 1.0.0 listed
			},
		}}},
		{id: "Newtonsoft.Json", version: "13.0.3"},
		{id: "OldLib", version: "1.0.0"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}

	got := map[string]Status{}
	for _, f := range r.Findings {
		got[f.DependencyID] = f.Status
	}
	want := map[string]Status{
		"Newtonsoft.Json":      StatusOK,
		"DoesNotExist.Package": StatusMissing,
		"OldLib":               StatusUnsatisfied,
	}
	for id, wantStatus := range want {
		if got[id] != wantStatus {
			t.Errorf("dependency %q status = %q, want %q", id, got[id], wantStatus)
		}
	}

	ok, missing, unsatisfied := r.Counts()
	if ok != 1 || missing != 1 || unsatisfied != 1 {
		t.Errorf("Counts() = (%d,%d,%d), want (1,1,1)", ok, missing, unsatisfied)
	}
	if r.ExitCode() != 1 {
		t.Errorf("ExitCode() = %d, want 1 (findings present)", r.ExitCode())
	}
}

func TestRun_UnlistedVersionDoesNotSatisfy(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{
			deps: []dep{{id: "Lib", rng: "2.0.0"}},
		}}},
		{id: "Lib", version: "1.0.0"},
		{id: "Lib", version: "2.0.0", unlisted: true}, // exists, but unlisted
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	var got Status
	for _, f := range r.Findings {
		if f.DependencyID == "Lib" {
			got = f.Status
		}
	}
	if got != StatusUnsatisfied {
		t.Errorf("Lib status = %q, want %q (2.0.0 exists but is unlisted)", got, StatusUnsatisfied)
	}
}

func TestRun_NoDependencies_NoFindings(t *testing.T) {
	packages := []pkg{{id: "Standalone", version: "1.0.0"}}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if len(r.Findings) != 0 {
		t.Errorf("Findings = %v, want none", r.Findings)
	}
	if r.ExitCode() != 0 {
		t.Errorf("ExitCode() = %d, want 0", r.ExitCode())
	}
}

func TestRun_PackageOption_ScansOnlyThatPackage(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Lib", rng: "1.0.0"}}}}},
		{id: "Other", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Missing", rng: "1.0.0"}}}}},
		{id: "Lib", version: "1.0.0"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{Package: "App"})

	if len(r.Findings) != 1 || r.Findings[0].Package != "App" {
		t.Fatalf("Findings = %+v, want exactly one finding for App", r.Findings)
	}
	if r.Findings[0].Status != StatusOK {
		t.Errorf("status = %q, want ok", r.Findings[0].Status)
	}
}

func TestRun_PackageOption_NotFound_Aborts(t *testing.T) {
	srv := fakeRegFeed(t, []pkg{{id: "App", version: "1.0.0"}})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{Package: "NoSuchPackage"})

	if !r.Aborted {
		t.Fatal("expected Aborted for a nonexistent --package target")
	}
	if r.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", r.ExitCode())
	}
}

func TestRun_MalformedRange_ReportsUnsatisfied(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Lib", rng: "not-a-version"}}}}},
		{id: "Lib", version: "1.0.0"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if len(r.Findings) != 1 || r.Findings[0].Status != StatusUnsatisfied {
		t.Fatalf("Findings = %+v, want one unsatisfied finding for the malformed range", r.Findings)
	}
}

func TestRun_MultipleDependencyGroups_AllScanned(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{
			{targetFramework: "net472", deps: []dep{{id: "LibA", rng: "1.0.0"}}},
			{targetFramework: "net8.0", deps: []dep{{id: "LibB", rng: "1.0.0"}}},
		}},
		{id: "LibA", version: "1.0.0"},
		{id: "LibB", version: "1.0.0"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if len(r.Findings) != 2 {
		t.Fatalf("Findings = %+v, want 2 (one per target framework group)", r.Findings)
	}
	frameworks := map[string]string{}
	for _, f := range r.Findings {
		frameworks[f.DependencyID] = f.TargetFramework
	}
	if frameworks["LibA"] != "net472" || frameworks["LibB"] != "net8.0" {
		t.Errorf("target frameworks = %+v, want LibA=net472 LibB=net8.0", frameworks)
	}
}

// TestRun_OutOfLineRegistrationPage_StillScanned covers the standard NuGet
// v3 representation where a registration index page omits inline "items"
// and instead points at a separate page document via "@id". Before this
// was handled, the scan silently walked zero leaves for such a package and
// reported a clean result even though its dependency didn't resolve.
func TestRun_OutOfLineRegistrationPage_StillScanned(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Missing.Lib", rng: "1.0.0"}}}}},
	}
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{
		packages:  packages,
		outOfLine: map[string]bool{"app": true},
	})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	if len(r.Findings) != 1 || r.Findings[0].Status != StatusMissing {
		t.Fatalf("Findings = %+v, want one missing finding even though App's registration page was out-of-line", r.Findings)
	}
}

// TestRun_StableRangeExcludesPrereleaseCandidate covers NuGet's dependency
// resolution rule that a range with no prerelease bound (e.g. "[1.0,2.0)")
// must not be satisfied by a prerelease-only candidate.
func TestRun_StableRangeExcludesPrereleaseCandidate(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Lib", rng: "[1.0.0,2.0.0)"}}}}},
		{id: "Lib", version: "1.2.0-beta"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if len(r.Findings) != 1 || r.Findings[0].Status != StatusUnsatisfied {
		t.Fatalf("Findings = %+v, want unsatisfied: a stable range must not accept a prerelease-only candidate", r.Findings)
	}
}

// TestRun_PrereleaseRangeAcceptsPrereleaseCandidate is the companion case:
// a range that itself references a prerelease bound does accept a matching
// prerelease candidate.
func TestRun_PrereleaseRangeAcceptsPrereleaseCandidate(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Lib", rng: "[1.0.0-beta,2.0.0)"}}}}},
		{id: "Lib", version: "1.2.0-beta"},
	}
	srv := fakeRegFeed(t, packages)
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if len(r.Findings) != 1 || r.Findings[0].Status != StatusOK {
		t.Fatalf("Findings = %+v, want ok: the range explicitly opts into prerelease", r.Findings)
	}
}

// TestRun_DependencyRegistrationError_AbortsInsteadOfMissing covers the
// case where a dependency's registration request fails for a reason other
// than "not found" (network error, 401, 500, ...). That must abort with
// exit code 2, not silently report the dependency as missing.
func TestRun_DependencyRegistrationError_AbortsInsteadOfMissing(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Flaky", rng: "1.0.0"}}}}},
	}
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{
		packages:   packages,
		failStatus: map[string]int{"flaky": http.StatusInternalServerError},
	})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{})

	if !r.Aborted {
		t.Fatalf("expected Aborted when a dependency's registration request fails with a server error, got Findings=%+v", r.Findings)
	}
	if r.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", r.ExitCode())
	}
}

// TestRun_MaxRPS_PacesRequests verifies Options.MaxRPS actually throttles
// the scan: fetching four packages' registrations at 10 req/s should take
// meaningfully longer than doing so unthrottled.
func TestRun_MaxRPS_PacesRequests(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{
			{id: "LibA", rng: "1.0.0"},
			{id: "LibB", rng: "1.0.0"},
			{id: "LibC", rng: "1.0.0"},
		}}}},
		{id: "LibA", version: "1.0.0"},
		{id: "LibB", version: "1.0.0"},
		{id: "LibC", version: "1.0.0"},
	}
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{packages: packages})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	const rps = 10.0 // 100ms between requests
	start := time.Now()
	r := Run(c, Options{MaxRPS: rps})
	elapsed := time.Since(start)

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	// 4 registration fetches (App, LibA, LibB, LibC) paced 100ms apart
	// means at least ~300ms of enforced spacing between them.
	if want := 300 * time.Millisecond; elapsed < want {
		t.Errorf("elapsed = %v, want at least %v with MaxRPS=%v", elapsed, want, rps)
	}
}

// TestRun_MaxRPS_Zero_IsUnthrottled verifies the default (MaxRPS: 0)
// preserves the pre-existing unthrottled behavior.
func TestRun_MaxRPS_Zero_IsUnthrottled(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{
			{id: "LibA", rng: "1.0.0"},
			{id: "LibB", rng: "1.0.0"},
		}}}},
		{id: "LibA", version: "1.0.0"},
		{id: "LibB", version: "1.0.0"},
	}
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{packages: packages})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	start := time.Now()
	r := Run(c, Options{})
	elapsed := time.Since(start)

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("elapsed = %v, want well under 250ms unthrottled", elapsed)
	}
}

// TestRun_RetriesOn503ThenSucceeds verifies a transient 503 (e.g. from a
// feed's own rate limiter) is retried rather than aborting the whole scan.
func TestRun_RetriesOn503ThenSucceeds(t *testing.T) {
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Flaky", rng: "1.0.0"}}}}},
		{id: "Flaky", version: "1.0.0"},
	}
	srv, probe := fakeRegFeedWithConfig(t, regFeedConfig{
		packages:       packages,
		flakyStatus:    map[string]int{"flaky": http.StatusServiceUnavailable},
		flakyFailCount: map[string]int{"flaky": 1}, // fails once, succeeds on the retry
	})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{Package: "App"})

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	if len(r.Findings) != 1 || r.Findings[0].Status != StatusOK {
		t.Fatalf("Findings = %+v, want ok after the transient 503 was retried", r.Findings)
	}

	probe.mu.Lock()
	defer probe.mu.Unlock()
	if probe.attempts["flaky"] != 2 {
		t.Errorf("attempts for flaky = %d, want 2 (1 failure + 1 successful retry)", probe.attempts["flaky"])
	}
}

// TestRun_HonorsRetryAfterHeader verifies a 429's Retry-After header
// overrides the default exponential backoff.
func TestRun_HonorsRetryAfterHeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ~1s Retry-After test in -short mode")
	}
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "Flaky", rng: "1.0.0"}}}}},
		{id: "Flaky", version: "1.0.0"},
	}
	srv, _ := fakeRegFeedWithConfig(t, regFeedConfig{
		packages:       packages,
		flakyStatus:    map[string]int{"flaky": http.StatusTooManyRequests},
		flakyFailCount: map[string]int{"flaky": 1},
		retryAfter:     map[string]string{"flaky": "1"},
	})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	start := time.Now()
	r := Run(c, Options{Package: "App"})
	elapsed := time.Since(start)

	if r.Aborted {
		t.Fatalf("unexpected abort: %s", r.AbortReason)
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("elapsed = %v, want at least ~1s honoring the feed's Retry-After instead of the shorter default backoff", elapsed)
	}
}

// TestRun_GivesUpAfterMaxRetries verifies a persistently failing 503
// eventually aborts rather than retrying forever.
func TestRun_GivesUpAfterMaxRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping several-second max-retries backoff test in -short mode")
	}
	packages := []pkg{
		{id: "App", version: "1.0.0", deps: []depGroup{{deps: []dep{{id: "AlwaysFlaky", rng: "1.0.0"}}}}},
		{id: "AlwaysFlaky", version: "1.0.0"},
	}
	srv, probe := fakeRegFeedWithConfig(t, regFeedConfig{
		packages:       packages,
		flakyStatus:    map[string]int{"alwaysflaky": http.StatusServiceUnavailable},
		flakyFailCount: map[string]int{"alwaysflaky": 1000}, // never recovers
	})
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := Run(c, Options{Package: "App"})

	if !r.Aborted {
		t.Fatalf("expected Aborted after exhausting retries on a persistent 503, got Findings=%+v", r.Findings)
	}

	probe.mu.Lock()
	defer probe.mu.Unlock()
	if want := maxRetries + 1; probe.attempts["alwaysflaky"] != want {
		t.Errorf("attempts = %d, want %d (1 initial + %d retries)", probe.attempts["alwaysflaky"], want, maxRetries)
	}
}
