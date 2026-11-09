package verify

import (
	"testing"

	"github.com/mwtrigg/nugctl/internal/client"
)

// runFull runs the whole verify (readonly, negative, push) against feed.
func runFull(t *testing.T, feed *fakeFeed) *Report {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	fastPolls(t)
	srv := feed.server()
	t.Cleanup(srv.Close)
	c := client.New(srv.URL, "test-key", false, false, true)
	return Run(c, Options{Push: true})
}

func wantStatuses(t *testing.T, r *Report, want map[string]Status) {
	t.Helper()
	for name, st := range want {
		if got := checkStatus(r, name); got != st {
			detail := ""
			for _, c := range r.Checks {
				if c.Name == name {
					detail = c.Detail + " " + c.Err
				}
			}
			t.Errorf("%s = %q, want %s (%s)", name, got, st, detail)
		}
	}
}

func TestSearchChecks_CompliantFeed(t *testing.T) {
	r := runFull(t, newFakeFeed(false))
	wantStatuses(t, r, map[string]Status{
		"push prerelease-only package":                           StatusPass,
		"push SemVer 2.0.0 package":                              StatusPass,
		"search excludes prerelease by default":                  StatusPass,
		"search excludes SemVer 2.0.0 without semVerLevel":       StatusPass,
		"autocomplete excludes prerelease by default":            StatusPass,
		"autocomplete excludes SemVer 2.0.0 without semVerLevel": StatusPass,
		"clean up extra test packages":                           StatusPass,
	})
}

func TestSearchChecks_PrereleaseDefaultIgnored(t *testing.T) {
	feed := newFakeFeed(false)
	feed.ignorePrerelease = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"search excludes prerelease by default":       StatusFail,
		"autocomplete excludes prerelease by default": StatusFail,
	})
}

func TestSearchChecks_SemVerLevelIgnored(t *testing.T) {
	feed := newFakeFeed(false)
	feed.ignoreSemVerLevel = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"search excludes SemVer 2.0.0 without semVerLevel":       StatusFail,
		"autocomplete excludes SemVer 2.0.0 without semVerLevel": StatusFail,
		"search excludes prerelease by default":                  StatusPass,
	})
}

func TestSearchChecks_NoAutocompleteSkips(t *testing.T) {
	feed := newFakeFeed(false)
	feed.noAutocomplete = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"autocomplete: package ids":                   StatusSkip,
		"autocomplete excludes prerelease by default": StatusSkip,
	})
}

// seeded returns a compliant feed holding one listed package.
func seeded(t *testing.T) *fakeFeed {
	t.Helper()
	feed := newFakeFeed(false)
	data, err := buildNupkg("Seed.Pkg", "2.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	nuspec, _ := nuspecFromNupkg(data)
	feed.pkgs[fkey("Seed.Pkg", "2.0.0")] = &fakePkg{id: "Seed.Pkg", version: "2.0.0", nupkg: data, nuspec: nuspec, listed: true}
	return feed
}

func newTestClient(url string) *client.Client {
	return client.New(url, "test-key", false, false, true)
}

func runReadonlyOn(t *testing.T, feed *fakeFeed, opts Options) *Report {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	srv := feed.server()
	t.Cleanup(srv.Close)
	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runReadonly(c, opts, r)
	return r
}

func TestSearchShape(t *testing.T) {
	r := runReadonlyOn(t, seeded(t), Options{})
	wantStatuses(t, r, map[string]Status{
		"search result shape (versions[])": StatusPass,
		"autocomplete: package ids":        StatusPass,
		"autocomplete: package versions":   StatusPass,
	})

	feed := seeded(t)
	feed.noSearchVersions = true
	r = runReadonlyOn(t, feed, Options{})
	wantStatuses(t, r, map[string]Status{"search result shape (versions[])": StatusFail})
}

func TestSearchShape_EmptyFeedSkips(t *testing.T) {
	r := runReadonlyOn(t, newFakeFeed(false), Options{})
	wantStatuses(t, r, map[string]Status{
		"search result shape (versions[])": StatusSkip,
		"autocomplete: package versions":   StatusSkip,
	})
}

func TestSearchShapeProblems_MissingDownloads(t *testing.T) {
	id, v, at := "A", "1.0.0", "http://x/a/1.0.0.json"
	hits := 1
	res := &rawSearch{TotalHits: &hits, Data: &[]rawSearchItem{{ID: &id, Version: &v, Versions: &[]rawSearchVersion{{ID: &at, Version: &v}}}}}
	if p := searchShapeProblems(res); len(p) != 1 {
		t.Errorf("problems = %v, want exactly the missing downloads", p)
	}
}
