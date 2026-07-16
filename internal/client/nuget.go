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
)

type Client struct {
	BaseURL    string
	APIKey     string
	Verbose    bool
	Insecure   bool
	httpClient *http.Client
	index      *ServiceIndex
}

func New(baseURL, apiKey string, verbose, insecure bool) *Client {
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
		httpClient: &http.Client{Transport: transport},
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

func (c *Client) ServiceIndex() (*ServiceIndex, error) {
	if c.index != nil {
		return c.index, nil
	}
	return c.RefreshServiceIndex()
}

// RefreshServiceIndex re-fetches the service index, bypassing the cache
// ServiceIndex keeps after the first successful call.
func (c *Client) RefreshServiceIndex() (*ServiceIndex, error) {
	var idx ServiceIndex
	if err := c.get(c.BaseURL, &idx); err != nil {
		return nil, err
	}
	c.index = &idx
	return &idx, nil
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
	base, err := c.resourceURL("SearchQueryService")
	if err != nil {
		// BaGetter fallback
		base = c.BaseURL + "/v3/search"
	}
	u, _ := url.Parse(base)
	params := url.Values{}
	params.Set("q", q)
	params.Set("skip", fmt.Sprintf("%d", skip))
	params.Set("take", fmt.Sprintf("%d", take))
	if prerelease {
		params.Set("prerelease", "true")
	}
	u.RawQuery = params.Encode()
	var result SearchResult
	return &result, c.get(u.String(), &result)
}

// --- Registration ---

type RegistrationIndex struct {
	Count int                `json:"count"`
	Items []RegistrationPage `json:"items"`
}

type RegistrationPage struct {
	Count int                `json:"count"`
	Items []RegistrationLeaf `json:"items"`
}

type RegistrationLeaf struct {
	CatalogEntry CatalogEntry `json:"catalogEntry"`
}

type CatalogEntry struct {
	ID                       string `json:"id"`
	Version                  string `json:"version"`
	Description              string `json:"description"`
	Authors                  string `json:"authors"`
	Tags                     string `json:"tags"`
	Published                string `json:"published"`
	ProjectURL               string `json:"projectUrl,omitempty"`
	LicenseURL               string `json:"licenseUrl,omitempty"`
	RequireLicenseAcceptance bool   `json:"requireLicenseAcceptance,omitempty"`
	Summary                  string `json:"summary,omitempty"`
	Title                    string `json:"title,omitempty"`
	PackageHash              string `json:"packageHash,omitempty"`
	PackageHashAlgorithm     string `json:"packageHashAlgorithm,omitempty"`
	PackageSize              int    `json:"packageSize,omitempty"`
	IsPrerelease             bool   `json:"isPrerelease"`
	Listed                   bool   `json:"listed"`
}

func (c *Client) Registration(id string) (*RegistrationIndex, error) {
	base, err := c.resourceURL("RegistrationsBaseUrl")
	if err != nil {
		base = c.BaseURL + "/v3/registration"
	}
	regURL := strings.TrimRight(base, "/") + "/" + strings.ToLower(id) + "/index.json"
	var idx RegistrationIndex
	return &idx, c.get(regURL, &idx)
}

func (c *Client) RegistrationVersion(id, version string) (*CatalogEntry, error) {
	base, err := c.resourceURL("RegistrationsBaseUrl")
	if err != nil {
		base = c.BaseURL + "/v3/registration"
	}
	regURL := strings.TrimRight(base, "/") + "/" + strings.ToLower(id) + "/" + strings.ToLower(version) + ".json"
	var leaf RegistrationLeaf
	return &leaf.CatalogEntry, c.get(regURL, &leaf)
}

// --- Push ---

func (c *Client) Push(path string) error {
	base, err := c.resourceURL("PackagePublish")
	if err != nil {
		base = c.BaseURL + "/api/v2/package"
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("package", filepath.Base(path))
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, f); err != nil {
		return err
	}
	w.Close()

	if c.Verbose {
		fmt.Fprintf(os.Stderr, "PUT %s\n", base)
	}
	req, err := http.NewRequest(http.MethodPut, base, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if c.APIKey != "" {
		req.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return networkError(base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return categorize(resp.StatusCode, string(body), base)
	}
	return nil
}

// --- Pull ---

func (c *Client) Pull(id, version, outDir string) (string, error) {
	base, err := c.resourceURL("PackageBaseAddress")
	if err != nil {
		base = c.BaseURL + "/v3/package"
	}
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
		return "", err
	}
	if c.APIKey != "" {
		req.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", networkError(dlURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", categorize(resp.StatusCode, string(body), dlURL)
	}

	outFile := filepath.Join(outDir, fmt.Sprintf("%s.%s.nupkg", id, version))
	f, err := os.Create(outFile)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", err
	}
	return outFile, nil
}

// --- Delete ---

func (c *Client) Delete(id, version string) error {
	base, err := c.resourceURL("PackagePublish")
	if err != nil {
		base = c.BaseURL + "/api/v2/package"
	}
	delURL := fmt.Sprintf("%s/%s/%s", strings.TrimRight(base, "/"), id, version)
	_, err = c.doRequest(http.MethodDelete, delURL, nil, "")
	return err
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
	base, err := c.resourceURL("PackagePublish")
	if err != nil {
		base = c.BaseURL + "/api/v2/package"
	}
	url := fmt.Sprintf("%s/%s/%s/deprecations", strings.TrimRight(base, "/"), id, version)
	if c.Verbose {
		fmt.Fprintf(os.Stderr, "PUT %s\n", url)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return networkError(url, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusCreated:
		return nil
	default:
		return categorize(resp.StatusCode, string(respBody), url)
	}
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
	if c.APIKey != "" {
		req.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return networkError(rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return categorize(resp.StatusCode, string(body), rawURL)
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
	if c.APIKey != "" {
		req.Header.Set("X-NuGet-ApiKey", c.APIKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, networkError(rawURL, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, categorize(resp.StatusCode, string(respBody), rawURL)
	}
	return respBody, nil
}
