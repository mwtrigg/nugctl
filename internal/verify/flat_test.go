package verify

import "testing"

func TestNormalizeVersion(t *testing.T) {
	cases := map[string]string{
		"1.00.0.0":      "1.0.0",
		"1.0":           "1.0.0",
		"1.01.1":        "1.1.1",
		"1.00.0.1":      "1.0.0.1",
		"1.0.01.0":      "1.0.1",
		"1.0.7+r3456":   "1.0.7",
		"1.0.0-Beta.01": "1.0.0-Beta.01",
	}
	for in, want := range cases {
		if got := normalizeVersion(in); got != want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if got := shortForm("1.0.0"); got != "1.0" {
		t.Errorf("shortForm(1.0.0) = %q", got)
	}
}

func TestFlatChecks_CompliantFeed(t *testing.T) {
	r := runFull(t, newFakeFeed(false))
	wantStatuses(t, r, map[string]Status{
		"push non-normalized version":              StatusPass,
		"version normalized: flat container":       StatusPass,
		"version normalized: registration":         StatusPass,
		"download via normalized version":          StatusPass,
		"nuspec download matches pushed package":   StatusPass,
		"duplicate push of an equivalent version":  StatusPass,
		"unlisted version stays in flat container": StatusPass,
	})
}

func TestFlatChecks_RawVersionsStored(t *testing.T) {
	feed := newFakeFeed(false)
	feed.keepRawVersions = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"version normalized: flat container":      StatusFail,
		"version normalized: registration":        StatusFail,
		"duplicate push of an equivalent version": StatusFail,
	})
}

func TestFlatChecks_OverwriteIsWarn(t *testing.T) {
	feed := newFakeFeed(false)
	feed.allowOverwrite = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{"duplicate push of an equivalent version": StatusWarn})
}

func TestFlatChecks_RewrittenNuspecIsWarn(t *testing.T) {
	feed := newFakeFeed(false)
	feed.rewriteNuspec = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{"nuspec download matches pushed package": StatusWarn})
}

// TestFlatChecks_UnlistedDropped exercises the check directly: on a feed
// that drops unlisted versions entirely the round trip's package no longer
// downloads, so the full run takes the hard-delete branch instead.
func TestFlatChecks_UnlistedDropped(t *testing.T) {
	feed2 := seeded(t)
	feed2.dropUnlisted = true
	feed2.pkgs[fkey("Seed.Pkg", "2.0.0")].listed = false
	feed2.pkgs[fkey("Seed.Pkg", "1.0.0")] = &fakePkg{id: "Seed.Pkg", version: "1.0.0", listed: true}
	t.Setenv("HOME", t.TempDir())
	srv := feed2.server()
	defer srv.Close()
	rep := &Report{}
	checkUnlistedInFlat(newTestClient(srv.URL), rep, "Seed.Pkg", "2.0.0")
	wantStatuses(t, rep, map[string]Status{"unlisted version stays in flat container": StatusFail})
}

func TestNuspecDownload_Readonly(t *testing.T) {
	r := runReadonlyOn(t, seeded(t), Options{Package: "Seed.Pkg"})
	wantStatuses(t, r, map[string]Status{"flat container .nuspec download": StatusPass})
	r = runReadonlyOn(t, seeded(t), Options{Package: "Other.Pkg"})
	if got := checkStatus(r, "flat container .nuspec download"); got == StatusPass {
		t.Errorf("nuspec check passed for a package the feed doesn't have")
	}
}
