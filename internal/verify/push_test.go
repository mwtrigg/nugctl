package verify

import (
	"strings"
	"testing"
	"time"

	"github.com/mwtrigg/nugctl/internal/client"
)

func TestRunPush_FullRoundTrip_UnlistOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false) // unlist-only, like BaGetter's default
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true}, r)

	statuses := map[string]Status{}
	for _, chk := range r.Checks {
		statuses[chk.Name] = chk.Status
	}
	want := map[string]Status{
		"build minimal package":                  StatusPass,
		"push package":                           StatusPass,
		"package appears in search/registration": StatusPass,
		"download matches pushed hash":           StatusPass,
		"unlist/delete package":                  StatusPass,
		"package disappears from default search": StatusPass,
		"feed delete support":                    StatusWarn,
		"relist package":                         StatusPass,
		"relisted package listed and in search":  StatusPass,
		"re-unlist after relist":                 StatusPass,
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
	runPush(c, Options{Push: true}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "feed delete support" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("feed delete support = %s, want pass for a hard-delete feed", got)
	}
	if got := checkStatus(r, "relist package"); got != StatusSkip {
		t.Errorf("relist package = %s, want skip on a hard-delete feed", got)
	}
	if feed.relists != 0 {
		t.Errorf("relist POSTs = %d, want 0 on a hard-delete feed", feed.relists)
	}
}

// TestRunPush_UnlistOnly_RelistUnsupported verifies a feed that unlists on
// DELETE but rejects PackagePublish's POST relist fails the relist check.
func TestRunPush_UnlistOnly_RelistUnsupported(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false)
	feed.noRelist = true
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true}, r)

	if got := checkStatus(r, "relist package"); got != StatusFail {
		t.Errorf("relist package = %s, want fail when POST relist is rejected", got)
	}
}

func checkStatus(r *Report, name string) Status {
	for _, chk := range r.Checks {
		if chk.Name == name {
			return chk.Status
		}
	}
	return ""
}

// TestRunPush_DefaultMode_RecentDownloadGuard_FailsCleanupNoRetry verifies
// that against a feed like Barn (409 on deleting a just-downloaded package
// unless forced), the default --push mode sends no force=true and does not
// retry: the cleanup check simply fails.
func TestRunPush_DefaultMode_RecentDownloadGuard_FailsCleanupNoRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false)
	feed.guardRecentDL = true
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "unlist/delete package" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("unlist/delete package = %s, want fail (409 with no force)", got)
	}

	deletes := feed.mainDeletes()
	if len(deletes) != 1 {
		t.Fatalf("expected exactly one DELETE attempt (no retry), got %d: %v", len(deletes), deletes)
	}
	if strings.Contains(deletes[0], "force=true") {
		t.Errorf("default mode must not send force=true, got %q", deletes[0])
	}
}

// TestRunPush_ForceDelete_SendsForceTrue verifies --force-delete appends
// force=true and the round-trip's remaining checks still run normally.
func TestRunPush_ForceDelete_SendsForceTrue(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false)
	feed.guardRecentDL = true
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true, ForceDelete: true}, r)

	statuses := map[string]Status{}
	for _, chk := range r.Checks {
		statuses[chk.Name] = chk.Status
	}
	want := map[string]Status{
		"unlist/delete package":                  StatusPass,
		"package disappears from default search": StatusPass,
	}
	for name, wantStatus := range want {
		if statuses[name] != wantStatus {
			t.Errorf("check %q = %s, want %s", name, statuses[name], wantStatus)
		}
	}

	// Two DELETEs of the round trip's package on an unlist-only feed: the
	// cleanup delete, then the re-unlist after the relist check. Both, and
	// the extra checks' cleanup deletes, must carry force=true.
	if deletes := feed.mainDeletes(); len(deletes) != 2 {
		t.Fatalf("expected two DELETEs (delete, re-unlist after relist), got %v", deletes)
	}
	feed.mu.Lock()
	defer feed.mu.Unlock()
	for _, q := range feed.deletes {
		if !strings.Contains(q, "force=true") {
			t.Errorf("expected every DELETE to carry force=true, got %v", feed.deletes)
		}
	}
}

// TestRunPush_ForceDelete_FailureStaysVisible verifies a forced delete that
// still fails is reported as a failed check, not silently swallowed.
func TestRunPush_ForceDelete_FailureStaysVisible(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false)
	feed.rejectAllDeletes = true
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true, ForceDelete: true}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "unlist/delete package" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("unlist/delete package = %s, want fail even when forced", got)
	}
}

// TestRunPush_NormalFeedUnaffectedByForceDelete verifies --force-delete is a
// no-op against a feed with no recent-download guard: the delete succeeds
// and the round trip completes as usual.
func TestRunPush_NormalFeedUnaffectedByForceDelete(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	feed := newFakeFeed(false)
	srv := feed.server()
	defer srv.Close()

	c := client.New(srv.URL, "test-key", false, false, true)
	r := &Report{}
	runPush(c, Options{Push: true, ForceDelete: true}, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "unlist/delete package" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("unlist/delete package = %s, want pass on a normal feed", got)
	}
}

func TestPollUntil_TimesOutWithoutHanging(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ~30s poll-timeout test in -short mode")
	}
	start := time.Now()
	got := pollUntil(func() bool { return false })
	if got {
		t.Fatal("expected pollUntil to return false when cond never succeeds")
	}
	if time.Since(start) < pollTimeout {
		t.Fatalf("pollUntil returned before the timeout elapsed: %s", time.Since(start))
	}
}
