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
	mu               sync.Mutex
	packages         map[string][]byte // "id/version" -> nupkg bytes
	unlisted         map[string]bool
	hardDelete       bool // if true, Delete removes the package entirely
	guardRecentDL    bool // if true, mimic Barn: reject deleting a just-downloaded package unless force=true
	rejectAllDeletes bool // if true, every DELETE fails with 409 regardless of force
	downloaded       map[string]bool
	deleteQueries    []string // raw query string of every DELETE request received, in order
}

func newFakeFeed(hardDelete bool) *fakeFeed {
	return &fakeFeed{packages: map[string][]byte{}, unlisted: map[string]bool{}, downloaded: map[string]bool{}, hardDelete: hardDelete}
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
		if ok {
			f.downloaded[key] = true
		}
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
		f.deleteQueries = append(f.deleteQueries, r.URL.RawQuery)
		if f.rejectAllDeletes {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"https://example.com/probs/recent-download","title":"package was recently downloaded","status":409}`)
			return
		}
		if f.guardRecentDL && f.downloaded[key] && r.URL.Query().Get("force") != "true" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"https://example.com/probs/recent-download","title":"package was recently downloaded","status":409}`)
			return
		}
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

// parseNupkgFilename splits "{id}.{version}.nupkg" back into id and version.
// Test IDs are always "nugctl-verify-<unix-ts>" (no dots), so the first "."
// that starts a numeric run marks the id/version boundary.
func parseNupkgFilename(name string) (id, version string) {
	name = strings.TrimSuffix(name, ".nupkg")
	for j := 0; j < len(name); j++ {
		if name[j] == '.' && j+1 < len(name) && name[j+1] >= '0' && name[j+1] <= '9' {
			return name[:j], name[j+1:]
		}
	}
	return name, ""
}

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

	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.deleteQueries) != 1 {
		t.Fatalf("expected exactly one DELETE attempt (no retry), got %d: %v", len(feed.deleteQueries), feed.deleteQueries)
	}
	if strings.Contains(feed.deleteQueries[0], "force=true") {
		t.Errorf("default mode must not send force=true, got query %q", feed.deleteQueries[0])
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

	feed.mu.Lock()
	defer feed.mu.Unlock()
	if len(feed.deleteQueries) != 1 || !strings.Contains(feed.deleteQueries[0], "force=true") {
		t.Errorf("expected exactly one DELETE with force=true, got %v", feed.deleteQueries)
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
