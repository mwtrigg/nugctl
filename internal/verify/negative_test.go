package verify

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mwtrigg/nugctl/internal/client"
)

func TestRunNegative_WellFormed404_Passes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "404 shape for nonexistent package" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("404 shape check = %s, want pass", got)
	}
}

func TestRunNegative_MalformedEmptyOK_Fails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		w.Write([]byte(`{"count":0,"items":[]}`)) // 200 instead of 404
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "404 shape for nonexistent package" {
			got = chk.Status
		}
	}
	if got != StatusFail {
		t.Errorf("404 shape check = %s, want fail for a 200 response", got)
	}
}

func TestRunNegative_NoCredentials_SkipsAuthCheck(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "", false, false, true) // no API key, no basic auth
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "401/403 for garbled credentials" {
			got = chk.Status
		}
	}
	if got != StatusSkip {
		t.Errorf("auth check = %s, want skip when no credentials are configured", got)
	}
}

func TestRunNegative_GarbledCredentials_404IsWarnNotFail(t *testing.T) {
	// A server that checks package existence before auth will 404 here
	// regardless of the (bad) credentials — that's not proof it accepts bad
	// credentials, just that this probe is inconclusive. Must not be a fail.
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "the-real-key", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "401/403 for garbled credentials" {
			got = chk.Status
		}
	}
	if got != StatusWarn {
		t.Errorf("auth check = %s, want warn (existence-before-auth ordering is spec-legal, not a failure)", got)
	}
}

func TestRunNegative_GarbledCredentials_ExpectsAuthRejection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		if r.Method == http.MethodDelete {
			if r.Header.Get("X-NuGet-ApiKey") != "the-real-key" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := client.New(srv.URL, "the-real-key", false, false, true)
	r := &Report{}
	runNegative(c, r)

	var got Status
	for _, chk := range r.Checks {
		if chk.Name == "401/403 for garbled credentials" {
			got = chk.Status
		}
	}
	if got != StatusPass {
		t.Errorf("auth check = %s, want pass (server correctly rejected garbled credentials)", got)
	}
}
