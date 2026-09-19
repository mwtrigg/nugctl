package deps

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	failStatus map[string]int  // id (lowercased) -> HTTP status the registration endpoint returns instead of content
}

// fakeRegFeed serves only the two v3 resources deps.Run needs: search (for
// full-feed discovery) and registration (for dependencyGroups).
func fakeRegFeed(t *testing.T, packages []pkg) *httptest.Server {
	return fakeRegFeedWithConfig(t, regFeedConfig{packages: packages})
}

func fakeRegFeedWithConfig(t *testing.T, cfg regFeedConfig) *httptest.Server {
	t.Helper()
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
	return srv
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
	srv := fakeRegFeedWithConfig(t, regFeedConfig{
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
	srv := fakeRegFeedWithConfig(t, regFeedConfig{
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
