package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetAuth_BasicAuthOnly(t *testing.T) {
	c := New("https://feed.example", "", false, false, true)
	c.BasicAuthUser = "svc-nuget"
	c.BasicAuthPass = "hunter2"

	req, _ := http.NewRequest(http.MethodGet, "https://feed.example/v3/index.json", nil)
	c.setAuth(req)

	user, pass, ok := req.BasicAuth()
	if !ok || user != "svc-nuget" || pass != "hunter2" {
		t.Fatalf("BasicAuth() = %q/%q, %v; want svc-nuget/hunter2, true", user, pass, ok)
	}
	if req.Header.Get("X-NuGet-ApiKey") != "" {
		t.Errorf("expected no API key header, got %q", req.Header.Get("X-NuGet-ApiKey"))
	}
}

func TestSetAuth_APIKeyAndBasicAuthTogether(t *testing.T) {
	c := New("https://feed.example", "my-api-key", false, false, true)
	c.BasicAuthUser = "svc-nuget"
	c.BasicAuthPass = "hunter2"

	req, _ := http.NewRequest(http.MethodGet, "https://feed.example/v3/index.json", nil)
	c.setAuth(req)

	if req.Header.Get("X-NuGet-ApiKey") != "my-api-key" {
		t.Errorf("X-NuGet-ApiKey = %q, want my-api-key", req.Header.Get("X-NuGet-ApiKey"))
	}
	if user, pass, ok := req.BasicAuth(); !ok || user != "svc-nuget" || pass != "hunter2" {
		t.Errorf("BasicAuth() = %q/%q, %v; want svc-nuget/hunter2, true", user, pass, ok)
	}
}

func TestSetAuth_Neither(t *testing.T) {
	c := New("https://feed.example", "", false, false, true)
	req, _ := http.NewRequest(http.MethodGet, "https://feed.example/v3/index.json", nil)
	c.setAuth(req)

	if req.Header.Get("X-NuGet-ApiKey") != "" {
		t.Errorf("expected no API key header, got %q", req.Header.Get("X-NuGet-ApiKey"))
	}
	if _, _, ok := req.BasicAuth(); ok {
		t.Error("expected no Basic Auth header")
	}
}

func TestSearch_SendsBasicAuthCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var gotUser, gotPass string
	var gotOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			// No SearchQueryService resource, so Search falls back to
			// BaseURL + "/v3/search" - still routed through this server.
			w.Write([]byte(`{"version":"3.0.0","resources":[]}`))
			return
		}
		gotUser, gotPass, gotOK = r.BasicAuth()
		w.Write([]byte(`{"totalHits":0,"data":[]}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	c.BasicAuthUser = "svc-nuget"
	c.BasicAuthPass = "hunter2"

	if _, err := c.Search("newtonsoft", 0, 10, false); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !gotOK || gotUser != "svc-nuget" || gotPass != "hunter2" {
		t.Fatalf("BasicAuth on search request = %q/%q, %v; want svc-nuget/hunter2, true", gotUser, gotPass, gotOK)
	}
}
