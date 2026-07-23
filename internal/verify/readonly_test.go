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
	for _, rt := range requiredResourceTypes {
		found := false
		for _, chk := range r.Checks {
			if chk.Name == "resource present: "+rt {
				found = true
				if chk.Status != StatusPass {
					t.Errorf("resource present: %s = %s, want pass", rt, chk.Status)
				}
			}
		}
		if !found {
			t.Errorf("no check emitted for resource type %s", rt)
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
