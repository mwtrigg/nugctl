package client

import (
	"encoding/json"
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

// registrationFeed serves a registration index whose single page inlines its
// leaves (catalogEntry as an object, as the spec requires inside a page) and
// a standalone leaf document whose catalogEntry has the given raw JSON shape.
func registrationFeed(t *testing.T, standaloneCatalogEntry string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"3.0.0","resources":[{"@id":"` + srv.URL + `/reg/","@type":"RegistrationsBaseUrl/3.6.0"}]}`))
	})
	mux.HandleFunc("/reg/mypkg/index.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"count":1,"items":[{"@id":"` + srv.URL + `/reg/mypkg/index.json#page/1.0.0/1.0.0","count":1,"lower":"1.0.0","upper":"1.0.0","items":[
			{"@id":"` + srv.URL + `/reg/mypkg/1.0.0.json","catalogEntry":{"@id":"` + srv.URL + `/catalog/mypkg.1.0.0.json","id":"MyPkg","version":"1.0.0","listed":false}}
		]}]}`))
	})
	mux.HandleFunc("/reg/mypkg/1.0.0.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"@id":"` + srv.URL + `/reg/mypkg/1.0.0.json","listed":false,"catalogEntry":` + standaloneCatalogEntry + `}`))
	})
	srv = httptest.NewServer(mux)
	return srv
}

// TestRegistrationVersion_StandaloneLeafCatalogEntryString covers the spec'd
// shape: a standalone registration leaf's catalogEntry is a URL string
// (https://learn.microsoft.com/nuget/api/registration-base-url-resource#registration-leaf).
// The entry's metadata must then come from the registration index.
func TestRegistrationVersion_StandaloneLeafCatalogEntryString(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := registrationFeed(t, `"http://feed.example/catalog/mypkg.1.0.0.json"`)
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	e, err := c.RegistrationVersion("MyPkg", "1.0.0")
	if err != nil {
		t.Fatalf("RegistrationVersion: %v", err)
	}
	if e.ID != "MyPkg" || e.Version != "1.0.0" || e.Listed {
		t.Fatalf("entry = %+v, want id=MyPkg version=1.0.0 listed=false", e)
	}
}

// TestRegistrationVersion_StandaloneLeafCatalogEntryObject keeps accepting
// feeds (BaGetter, older Barn) that inline the catalog entry in the leaf.
func TestRegistrationVersion_StandaloneLeafCatalogEntryObject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := registrationFeed(t, `{"id":"MyPkg","version":"1.0.0","listed":false}`)
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	e, err := c.RegistrationVersion("MyPkg", "1.0.0")
	if err != nil {
		t.Fatalf("RegistrationVersion: %v", err)
	}
	if e.ID != "MyPkg" || e.Version != "1.0.0" || e.Listed {
		t.Fatalf("entry = %+v, want id=MyPkg version=1.0.0 listed=false", e)
	}
}

func TestRegistrationLeaf_UnmarshalBothCatalogEntryShapes(t *testing.T) {
	var s RegistrationLeaf
	if err := json.Unmarshal([]byte(`{"catalogEntry":"https://x/catalog/a.json"}`), &s); err != nil {
		t.Fatalf("string shape: %v", err)
	}
	if s.CatalogEntryURL != "https://x/catalog/a.json" || s.CatalogEntry.ID != "" {
		t.Fatalf("string shape decoded as %+v", s)
	}
	var o RegistrationLeaf
	if err := json.Unmarshal([]byte(`{"catalogEntry":{"@id":"https://x/catalog/a.json","id":"A","version":"1.0.0"}}`), &o); err != nil {
		t.Fatalf("object shape: %v", err)
	}
	if o.CatalogEntry.ID != "A" || o.CatalogEntryURL != "https://x/catalog/a.json" {
		t.Fatalf("object shape decoded as %+v", o)
	}
}
