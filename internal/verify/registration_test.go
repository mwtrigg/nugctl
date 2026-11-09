package verify

import "testing"

func TestRegistrationChecks_CompliantFeed(t *testing.T) {
	r := runFull(t, newFakeFeed(false))
	wantStatuses(t, r, map[string]Status{
		"original id casing: registration":                      StatusPass,
		"original id casing: search":                            StatusPass,
		"registration dependencies":                             StatusPass,
		"unlisted version stays in registration (listed:false)": StatusPass,
	})
}

func TestRegistrationChecks_LowercasedIDs(t *testing.T) {
	feed := newFakeFeed(false)
	feed.lowercaseIDs = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{
		"original id casing: registration": StatusFail,
		"original id casing: search":       StatusFail,
	})
}

func TestRegistrationChecks_DroppedDependencies(t *testing.T) {
	feed := newFakeFeed(false)
	feed.dropDeps = true
	r := runFull(t, feed)
	wantStatuses(t, r, map[string]Status{"registration dependencies": StatusFail})
}

func TestRegistrationChecks_UnlistedDropped(t *testing.T) {
	// Dropping unlisted versions everywhere makes the round trip's package
	// undownloadable, so the run sees a hard-delete feed. Exercise the
	// registration check directly instead.
	feed := seeded(t)
	feed.dropUnlisted = true
	feed.pkgs[fkey("Seed.Pkg", "1.0.0")] = &fakePkg{id: "Seed.Pkg", version: "1.0.0", listed: true}
	feed.pkgs[fkey("Seed.Pkg", "2.0.0")].listed = false
	t.Setenv("HOME", t.TempDir())
	fastPolls(t)
	srv := feed.server()
	defer srv.Close()
	r := &Report{}
	checkUnlistedInRegistration(newTestClient(srv.URL), r, "Seed.Pkg", "2.0.0")
	wantStatuses(t, r, map[string]Status{"unlisted version stays in registration (listed:false)": StatusFail})
}

func TestRegistrationReadonly(t *testing.T) {
	r := runReadonlyOn(t, seeded(t), Options{Package: "Seed.Pkg"})
	wantStatuses(t, r, map[string]Status{
		"registration required fields":                  StatusPass,
		"registration well-formed":                      StatusPass,
		"flat-container matches registration":           StatusPass,
		"package hash matches registration":             StatusPass,
		"registration gzip: RegistrationsBaseUrl/3.6.0": StatusPass,
	})
}

func TestRegistrationReadonly_OutOfLinePages(t *testing.T) {
	feed := seeded(t)
	feed.outOfLinePages = true
	r := runReadonlyOn(t, feed, Options{Package: "Seed.Pkg"})
	wantStatuses(t, r, map[string]Status{
		"registration required fields":        StatusPass,
		"registration well-formed":            StatusPass,
		"flat-container matches registration": StatusPass,
		"package hash matches registration":   StatusPass,
	})
	for _, c := range r.Checks {
		if c.Name == "registration required fields" && c.Detail != "id=Seed.Pkg, 1 page(s), 1 fetched out of line" {
			t.Errorf("detail = %q", c.Detail)
		}
	}
}

func TestRegistrationReadonly_MissingBounds(t *testing.T) {
	feed := seeded(t)
	feed.omitPageBounds = true
	r := runReadonlyOn(t, feed, Options{Package: "Seed.Pkg"})
	wantStatuses(t, r, map[string]Status{"registration required fields": StatusFail})
}

func TestRegistrationReadonly_NoGzipWarns(t *testing.T) {
	feed := seeded(t)
	feed.noGzip = true
	r := runReadonlyOn(t, feed, Options{Package: "Seed.Pkg"})
	wantStatuses(t, r, map[string]Status{"registration gzip: RegistrationsBaseUrl/3.6.0": StatusWarn})
}

func TestSameRange(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"[1.0.0, 2.0.0)", "[1.0.0,2.0.0)", true},
		{"[1.0.0, 2.0.0)", "[1.0, 2.0)", true},
		{"[1.0.0, 2.0.0)", "[1.0.0, 2.0.0]", false},
		{"[1.0.0, 2.0.0)", "1.0.0", false},
		{"1.0.0", "[1.0.0, )", true},
	}
	for _, tc := range cases {
		if got := sameRange(tc.a, tc.b); got != tc.want {
			t.Errorf("sameRange(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
