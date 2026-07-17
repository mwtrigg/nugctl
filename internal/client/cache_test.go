package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func serviceIndexJSON(searchURL string) []byte {
	idx := ServiceIndex{
		Version: "3.0.0",
		Resources: []Resource{
			{ID: searchURL, Type: "SearchQueryService/3.0.0"},
		},
	}
	data, _ := json.Marshal(idx)
	return data
}

func TestServiceIndex_DiskCacheAvoidsNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write(serviceIndexJSON("https://feed.example/search"))
	}))
	defer srv.Close()

	c1 := New(srv.URL, "", false, false, false)
	if _, err := c1.ServiceIndex(); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 network hit after cold fetch, got %d", got)
	}

	// A second, independent Client for the same feed URL should hit disk cache.
	c2 := New(srv.URL, "", false, false, false)
	if _, err := c2.ServiceIndex(); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected disk cache hit (still 1 network hit), got %d", got)
	}
}

func TestServiceIndex_NoCacheAlwaysHitsNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write(serviceIndexJSON("https://feed.example/search"))
	}))
	defer srv.Close()

	c1 := New(srv.URL, "", false, false, true)
	if _, err := c1.ServiceIndex(); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	c2 := New(srv.URL, "", false, false, true)
	if _, err := c2.ServiceIndex(); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected --no-cache to bypass disk cache (2 network hits), got %d", got)
	}
}

func TestRefreshServiceIndex_ConditionalGETRevalidatesStaleCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	const etag = `"v1"`
	var fullFetches, conditionalHits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			atomic.AddInt32(&conditionalHits, 1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		atomic.AddInt32(&fullFetches, 1)
		w.Header().Set("ETag", etag)
		w.Write(serviceIndexJSON("https://feed.example/search"))
	}))
	defer srv.Close()

	c := New(srv.URL, "", false, false, false)
	idx, err := c.ServiceIndex()
	if err != nil {
		t.Fatalf("cold fetch: %v", err)
	}
	if atomic.LoadInt32(&fullFetches) != 1 {
		t.Fatalf("expected 1 full fetch, got %d", fullFetches)
	}

	// Force staleness by rewriting the cache entry's FetchedAt into the past.
	entry := loadIndexCache(c.BaseURL)
	if entry == nil {
		t.Fatal("expected a cache entry to have been written")
	}
	entry.FetchedAt = time.Now().Add(-2 * indexCacheTTL)
	if err := saveIndexCache(c.BaseURL, entry); err != nil {
		t.Fatalf("rewriting cache entry: %v", err)
	}

	c2 := New(srv.URL, "", false, false, false)
	idx2, err := c2.ServiceIndex()
	if err != nil {
		t.Fatalf("revalidation fetch: %v", err)
	}
	if atomic.LoadInt32(&conditionalHits) != 1 {
		t.Fatalf("expected a conditional GET (304), got %d full=%d cond=%d", conditionalHits, fullFetches, conditionalHits)
	}
	if atomic.LoadInt32(&fullFetches) != 1 {
		t.Fatalf("304 should not trigger a second full fetch, got %d", fullFetches)
	}
	if idx2.Resources[0].ID != idx.Resources[0].ID {
		t.Fatalf("revalidated index = %+v, want same resources as %+v", idx2, idx)
	}
}

func TestWithResource_RetriesOnceOnStaleCachedResourceURL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(serviceIndexJSON(("http://updated.example/search")))
	}))
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	// Seed an in-memory index pointing at a stale resource URL, as if it had
	// been resolved earlier in the process (or loaded from an on-disk cache
	// that predates a feed reconfiguration).
	c.index = &ServiceIndex{Resources: []Resource{
		{ID: "http://stale.example/search", Type: "SearchQueryService/3.0.0"},
	}}

	var attempts []string
	err := c.withResource("SearchQueryService", "http://fallback.example", func(base string) error {
		attempts = append(attempts, base)
		if base == "http://stale.example/search" {
			return &ClientError{Category: CatNotFound, StatusCode: 404, URL: base}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected the retry to succeed, got %v", err)
	}
	if len(attempts) != 2 || attempts[1] != "http://updated.example/search" {
		t.Fatalf("attempts = %v, want [stale, updated]", attempts)
	}
}

func TestWithResource_NoRetryLoopWhenResourceURLUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(serviceIndexJSON("http://same.example/search"))
	}))
	defer srv.Close()

	c := New(srv.URL, "", false, false, true)
	c.index = &ServiceIndex{Resources: []Resource{
		{ID: "http://same.example/search", Type: "SearchQueryService/3.0.0"},
	}}

	var attempts int
	err := c.withResource("SearchQueryService", "http://fallback.example", func(base string) error {
		attempts++
		return &ClientError{Category: CatNotFound, StatusCode: 404, URL: base}
	})
	if !IsNotFound(err) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected exactly 1 attempt when the refreshed index doesn't change the URL, got %d", attempts)
	}
}
