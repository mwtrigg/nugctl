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
