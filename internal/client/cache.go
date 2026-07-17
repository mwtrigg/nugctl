package client

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// indexCacheTTL is how long a cached service index is trusted without
// revalidation. Resource URLs on a running feed change essentially never, so
// this just bounds how stale a cache can be if a feed is reconfigured.
const indexCacheTTL = 30 * time.Minute

// indexCacheEntry is the on-disk cache of a feed's service index, keyed by
// feed URL (not profile name — a profile's URL can be overridden per
// invocation via --url/env, so caching by URL avoids serving one feed's
// resources for another).
type indexCacheEntry struct {
	Index        ServiceIndex `json:"index"`
	FetchedAt    time.Time    `json:"fetchedAt"`
	ETag         string       `json:"etag,omitempty"`
	LastModified string       `json:"lastModified,omitempty"`
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "nugctl", "cache"), nil
}

func cachePath(feedURL string) (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(feedURL))
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".json"), nil
}

// loadIndexCache returns the cached entry for feedURL, or nil if there is
// none. A missing or corrupt cache file is treated as a miss, not an error.
func loadIndexCache(feedURL string) *indexCacheEntry {
	path, err := cachePath(feedURL)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var e indexCacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil
	}
	return &e
}

func saveIndexCache(feedURL string, e *indexCacheEntry) error {
	path, err := cachePath(feedURL)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ClearCache removes all cached service-index entries for every feed.
func ClearCache() error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}
