package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	BaseURL       string
	APIKey        string
	BasicAuthUser string
	BasicAuthPass string
	Verbose       bool
	Insecure      bool
	NoCache       bool
	httpClient    *http.Client
	index         *ServiceIndex
}

func New(baseURL, apiKey string, verbose, insecure, noCache bool) *Client {
	transport := &http.Transport{}
	if insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		fmt.Fprintln(os.Stderr, "warning: TLS certificate verification is disabled (--insecure)")
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		APIKey:     apiKey,
		Verbose:    verbose,
		Insecure:   insecure,
		NoCache:    noCache,
		httpClient: &http.Client{Transport: transport},
	}
}

// setAuth attaches whichever credentials the client is configured with: a
// NuGet API key header, HTTP Basic Auth (e.g. for a feed sitting behind a
// reverse proxy that gates access separately from the feed's own API key),
// or both at once.
func (c *Client) setAuth(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}
	if c.BasicAuthUser != "" {
		req.SetBasicAuth(c.BasicAuthUser, c.BasicAuthPass)
	}
}

// --- Service Index ---

type ServiceIndex struct {
	Version   string     `json:"version"`
	Resources []Resource `json:"resources"`
}

type Resource struct {
	ID      string `json:"@id"`
	Type    string `json:"@type"`
	Comment string `json:"comment,omitempty"`
}

// ServiceIndex returns the feed's service index, preferring (in order) the
// in-process cache, a fresh on-disk cache, and finally the network — a
// running feed's resource URLs change essentially never, so official NuGet
// clients treat the index as effectively static per feed.
func (c *Client) ServiceIndex() (*ServiceIndex, error) {
	if c.index != nil {
		return c.index, nil
	}
	if !c.NoCache {
		if entry := loadIndexCache(c.BaseURL); entry != nil && time.Since(entry.FetchedAt) < indexCacheTTL {
			idx := entry.Index
			c.index = &idx
			return c.index, nil
		}
	}
	return c.RefreshServiceIndex()
}

// RefreshServiceIndex re-fetches the service index, bypassing the in-process
// and on-disk cache freshness check (though it still uses a conditional GET
// against any on-disk ETag/Last-Modified, so a still-current feed costs a
// cheap 304 rather than a full response).
func (c *Client) RefreshServiceIndex() (*ServiceIndex, error) {
	var prevEntry *indexCacheEntry
	if !c.NoCache {
		prevEntry = loadIndexCache(c.BaseURL)
	}

	req, err := http.NewRequest(http.MethodGet, c.BaseURL, nil)
	if err != nil {
		return nil, err
	}
	if prevEntry != nil {
		if prevEntry.ETag != "" {
			req.Header.Set("If-None-Match", prevEntry.ETag)
		}
		if prevEntry.LastModified != "" {
			req.Header.Set("If-Modified-Since", prevEntry.LastModified)
		}
	}
	c.setAuth(req)
	req.Header.Set("Accept", "application/json")
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "GET %s\n", c.BaseURL)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, networkError(c.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && prevEntry != nil {
		idx := prevEntry.Index
		c.index = &idx
		prevEntry.FetchedAt = time.Now()
		_ = saveIndexCache(c.BaseURL, prevEntry)
		return c.index, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, categorizeResp(resp, string(body), c.BaseURL)
	}

	var idx ServiceIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, err
	}
	c.index = &idx
	if !c.NoCache {
		_ = saveIndexCache(c.BaseURL, &indexCacheEntry{
			Index:        idx,
			FetchedAt:    time.Now(),
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
		})
	}
	return c.index, nil
}

func (c *Client) resourceURL(typePrefix string) (string, error) {
	idx, err := c.ServiceIndex()
	if err != nil {
		return "", err
	}
	for _, r := range idx.Resources {
		if strings.HasPrefix(r.Type, typePrefix) {
			return r.ID, nil
		}
	}
	return "", fmt.Errorf("service index has no resource of type %q", typePrefix)
}

// withResource resolves the resource URL for typePrefix (falling back to
// fallback if the feed's service index has none) and runs action against it.
// If action fails with a 404, the cached service index may simply be stale
// (e.g. the feed was reconfigured behind an unchanged URL), so it's
// refreshed and action is retried exactly once before the error is returned —
// a stale cache degrades to one extra round trip, never a hard failure.
func (c *Client) withResource(typePrefix, fallback string, action func(base string) error) error {
	base, err := c.resourceURL(typePrefix)
	if err != nil {
		base = fallback
	}
	err = action(base)
	if err == nil || !IsNotFound(err) {
		return err
	}
	if _, rerr := c.RefreshServiceIndex(); rerr != nil {
		return err
	}
	retryBase, rerr := c.resourceURL(typePrefix)
	if rerr != nil {
		retryBase = fallback
	}
	if retryBase == base {
		return err
	}
	return action(retryBase)
}

// --- Search ---

type SearchResult struct {
	TotalHits int             `json:"totalHits"`
	Data      []SearchPackage `json:"data"`
}

type SearchPackage struct {
	ID             string   `json:"id"`
	Version        string   `json:"version"`
	Description    string   `json:"description"`
	Authors        []string `json:"authors"`
	Tags           []string `json:"tags"`
	TotalDownloads int      `json:"totalDownloads"`
	Verified       bool     `json:"verified"`
	ProjectURL     string   `json:"projectUrl,omitempty"`
	IconURL        string   `json:"iconUrl,omitempty"`
}

func (c *Client) Search(q string, skip, take int, prerelease bool) (*SearchResult, error) {
	var result SearchResult
	err := c.withResource("SearchQueryService", c.BaseURL+"/v3/search", func(base string) error {
		u, _ := url.Parse(base)
		params := url.Values{}
		params.Set("q", q)
		params.Set("skip", fmt.Sprintf("%d", skip))
		params.Set("take", fmt.Sprintf("%d", take))
		if prerelease {
			params.Set("prerelease", "true")
		}
		u.RawQuery = params.Encode()
		return c.get(u.String(), &result)
	})
	return &result, err
}

// --- Registration ---

type RegistrationIndex struct {
	Count int                `json:"count"`
	Items []RegistrationPage `json:"items"`
}

// RegistrationPage is one page of a registration index. A feed may inline a
// page's leaves (Items populated) or, per the NuGet v3 registration schema,
// leave a large page out-of-line: only ID and Count are present and the
// leaves must be fetched separately from ID. See RegistrationPageAt.
type RegistrationPage struct {
	ID    string             `json:"@id"`
	Count int                `json:"count"`
	Items []RegistrationLeaf `json:"items"`
}

// Inline reports whether the page's leaves were included directly, as
// opposed to needing a separate fetch via RegistrationPageAt(page.ID).
func (p RegistrationPage) Inline() bool {
	return len(p.Items) > 0 || p.Count == 0
}

type RegistrationLeaf struct {
	CatalogEntry CatalogEntry `json:"catalogEntry"`
}

// Tags unmarshals either shape a NuGet v3 feed may use for catalogEntry.tags:
// a space-delimited string (nuget.org) or a JSON array of strings (BaGetter).
type Tags []string

func (t *Tags) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*t = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*t = Tags(strings.Fields(s))
	return nil
}

type CatalogEntry struct {
	ID                       string            `json:"id"`
	Version                  string            `json:"version"`
	Description              string            `json:"description"`
	Authors                  string            `json:"authors"`
	Tags                     Tags              `json:"tags"`
	Published                string            `json:"published"`
	ProjectURL               string            `json:"projectUrl,omitempty"`
	LicenseURL               string            `json:"licenseUrl,omitempty"`
	RequireLicenseAcceptance bool              `json:"requireLicenseAcceptance,omitempty"`
	Summary                  string            `json:"summary,omitempty"`
	Title                    string            `json:"title,omitempty"`
	PackageHash              string            `json:"packageHash,omitempty"`
	PackageHashAlgorithm     string            `json:"packageHashAlgorithm,omitempty"`
	PackageSize              int               `json:"packageSize,omitempty"`
	IsPrerelease             bool              `json:"isPrerelease"`
	Listed                   bool              `json:"listed"`
	DependencyGroups         []DependencyGroup `json:"dependencyGroups,omitempty"`
}

// UnmarshalJSON applies the NuGet v3 registration schema's documented
// default: a missing "listed" property means the version is listed. Without
// this, Go's zero value would silently treat an absent property the same as
// an explicit "listed": false.
func (e *CatalogEntry) UnmarshalJSON(data []byte) error {
	type alias CatalogEntry
	aux := &struct{ *alias }{alias: (*alias)(e)}
	e.Listed = true
	return json.Unmarshal(data, aux)
}

// DependencyGroup is one <group targetFramework="..."> entry in a NuGet v3
// catalog entry's dependencyGroups. TargetFramework is empty for a
// framework-agnostic group.
type DependencyGroup struct {
	TargetFramework string       `json:"targetFramework,omitempty"`
	Dependencies    []Dependency `json:"dependencies,omitempty"`
}

// Dependency is one dependency within a DependencyGroup: a package ID and
// its NuGet version range (https://learn.microsoft.com/nuget/concepts/package-versioning#version-ranges).
type Dependency struct {
	ID    string `json:"id"`
	Range string `json:"range,omitempty"`
}

func (c *Client) Registration(id string) (*RegistrationIndex, error) {
	var idx RegistrationIndex
	err := c.withResource("RegistrationsBaseUrl", c.BaseURL+"/v3/registration", func(base string) error {
		regURL := strings.TrimRight(base, "/") + "/" + strings.ToLower(id) + "/index.json"
		return c.get(regURL, &idx)
	})
	return &idx, err
}

func (c *Client) RegistrationVersion(id, version string) (*CatalogEntry, error) {
	var leaf RegistrationLeaf
	err := c.withResource("RegistrationsBaseUrl", c.BaseURL+"/v3/registration", func(base string) error {
		regURL := strings.TrimRight(base, "/") + "/" + strings.ToLower(id) + "/" + strings.ToLower(version) + ".json"
		return c.get(regURL, &leaf)
	})
	return &leaf.CatalogEntry, err
}

// RegistrationPageAt fetches a registration page document by its own
// absolute URL (a RegistrationPage.ID), for pages a feed left out-of-line
// rather than inlining into the registration index.
func (c *Client) RegistrationPageAt(pageURL string) (*RegistrationPage, error) {
	var page RegistrationPage
	err := c.get(pageURL, &page)
	return &page, err
}

// --- Push ---

// PushBytes uploads package bytes already read into memory. filename is
// used for the multipart form's filename field only.
func (c *Client) PushBytes(filename string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("package", filename)
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	w.Close()
	contentType := w.FormDataContentType()
	payload := buf.Bytes()

	return c.withResource("PackagePublish", c.BaseURL+"/api/v2/package", func(base string) error {
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "PUT %s\n", base)
		}
		req, err := http.NewRequest(http.MethodPut, base, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", contentType)
		c.setAuth(req)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError(base, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return categorizeResp(resp, string(body), base)
		}
		return nil
	})
}

func (c *Client) Push(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return c.PushBytes(filepath.Base(path), data)
}

// --- Pull ---

// PullBytes downloads a package version's .nupkg bytes without writing to disk.
func (c *Client) PullBytes(id, version string) ([]byte, error) {
	var data []byte
	err := c.withResource("PackageBaseAddress", c.BaseURL+"/v3/package", func(base string) error {
		dlURL := fmt.Sprintf("%s/%s/%s/%s.%s.nupkg",
			strings.TrimRight(base, "/"),
			strings.ToLower(id),
			strings.ToLower(version),
			strings.ToLower(id),
			strings.ToLower(version),
		)

		if c.Verbose {
			fmt.Fprintf(os.Stderr, "GET %s\n", dlURL)
		}
		req, err := http.NewRequest(http.MethodGet, dlURL, nil)
		if err != nil {
			return err
		}
		c.setAuth(req)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			return networkError(dlURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			return categorizeResp(resp, string(body), dlURL)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		data = body
		return nil
	})
	return data, err
}

func (c *Client) Pull(id, version, outDir string) (string, error) {
	data, err := c.PullBytes(id, version)
	if err != nil {
		return "", err
	}
	f, err := os.Create(filepath.Join(outDir, fmt.Sprintf("%s.%s.nupkg", id, version)))
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return f.Name(), nil
}

// --- Flat container ---

type FlatContainerVersions struct {
	Versions []string `json:"versions"`
}

func (c *Client) FlatContainerVersions(id string) (*FlatContainerVersions, error) {
	var out FlatContainerVersions
	err := c.withResource("PackageBaseAddress", c.BaseURL+"/v3/package", func(base string) error {
		u := fmt.Sprintf("%s/%s/index.json", strings.TrimRight(base, "/"), strings.ToLower(id))
		return c.get(u, &out)
	})
	return &out, err
}

// --- Delete ---

func (c *Client) Delete(id, version string) error {
	return c.DeleteWithOptions(id, version, DeleteOptions{})
}

// DeleteOptions controls feed-specific delete behavior. Force is a
// nugctl-specific extension for feeds (e.g. Barn) that reject deletion of a
// recently-downloaded package with HTTP 409 unless the request explicitly
// opts into forced deletion; it is not part of the NuGet v3 protocol and
// ordinary feeds ignore it.
type DeleteOptions struct {
	Force bool
}

// DeleteWithOptions deletes id/version, appending "force=true" to the
// request only when opts.Force is set.
func (c *Client) DeleteWithOptions(id, version string, opts DeleteOptions) error {
	return c.withResource("PackagePublish", c.BaseURL+"/api/v2/package", func(base string) error {
		delURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(base, "/"), id, version)
		if opts.Force {
			delURL += "?force=true"
		}
		_, err := c.doRequest(http.MethodDelete, delURL, nil, "")
		return err
	})
}

// --- Deprecation ---

type DeprecationRequest struct {
	Versions                []string `json:"versions"`
	IsLegacy                bool     `json:"isLegacy,omitempty"`
	HasCriticalBugs         bool     `json:"hasCriticalBugs,omitempty"`
	IsOther                 bool     `json:"isOther,omitempty"`
	Message                 string   `json:"message,omitempty"`
	AlternatePackageID      string   `json:"alternatePackageId,omitempty"`
	AlternatePackageVersion string   `json:"alternatePackageVersion,omitempty"`
}

func (c *Client) Deprecate(id, version string, req DeprecationRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return c.withResource("PackagePublish", c.BaseURL+"/api/v2/package", func(base string) error {
		depURL := fmt.Sprintf("%s/%s/%s/deprecations", strings.TrimRight(base, "/"), id, version)
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "PUT %s\n", depURL)
		}
		httpReq, err := http.NewRequest(http.MethodPut, depURL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		c.setAuth(httpReq)

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return networkError(depURL, err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)

		switch resp.StatusCode {
		case http.StatusOK, http.StatusNoContent, http.StatusCreated:
			return nil
		default:
			return categorizeResp(resp, string(respBody), depURL)
		}
	})
}

func (c *Client) Undeprecate(id, version string) error {
	// Clearing deprecation uses the same endpoint with an empty body (no reasons set).
	return c.Deprecate(id, version, DeprecationRequest{Versions: []string{version}})
}

// --- HTTP helpers ---

func (c *Client) get(rawURL string, out interface{}) error {
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "GET %s\n", rawURL)
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return networkError(rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return categorizeResp(resp, string(body), rawURL)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) doRequest(method, rawURL string, body []byte, contentType string) ([]byte, error) {
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "%s %s\n", method, rawURL)
	}
	var req *http.Request
	var err error
	if body != nil {
		req, err = http.NewRequest(method, rawURL, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(method, rawURL, nil)
	}
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	c.setAuth(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, networkError(rawURL, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, categorizeResp(resp, string(respBody), rawURL)
	}
	return respBody, nil
}
