package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/mwtrigg/nugctl/internal/nugetversion"
)

// normalizeVersion returns NuGet's normalized form of v: leading zeros
// dropped from each number, a zero fourth part omitted, and SemVer 2.0.0
// build metadata removed
// (https://learn.microsoft.com/nuget/concepts/package-versioning#normalized-version-numbers).
// The prerelease label keeps its case. Unparseable input comes back as is.
func normalizeVersion(v string) string {
	p, err := nugetversion.Parse(v)
	if err != nil {
		return v
	}
	s := fmt.Sprintf("%d.%d.%d", p.Major, p.Minor, p.Patch)
	if p.Revision != 0 {
		s += fmt.Sprintf(".%d", p.Revision)
	}
	if p.Prerelease != "" {
		s += "-" + p.Prerelease
	}
	return s
}

// sameVersion reports whether a and b are the same NuGet version: equal
// after normalization, compared case-insensitively.
func sameVersion(a, b string) bool {
	return strings.EqualFold(normalizeVersion(a), normalizeVersion(b))
}

// isSemVer2 reports whether v is SemVer 2.0.0-specific: a dot-separated
// prerelease label or build metadata
// (https://learn.microsoft.com/nuget/concepts/package-versioning#semantic-versioning-200).
func isSemVer2(v string) bool {
	if strings.Contains(v, "+") {
		return true
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return strings.Contains(v[i+1:], ".")
	}
	return false
}

// getJSON GETs rawURL and decodes a 2xx JSON body into out. It returns the
// raw body too, so callers can re-decode it into stricter shapes.
func getJSON(c *client.Client, rawURL string, out any) ([]byte, error) {
	resp, err := c.Raw(http.MethodGet, rawURL, http.Header{"Accept": {"application/json"}}, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.Body, client.HTTPError(resp, rawURL)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return resp.Body, fmt.Errorf("decoding %s: %w", rawURL, err)
		}
	}
	return resp.Body, nil
}

// joinURL appends path segments to base with exactly one slash between.
func joinURL(base string, segs ...string) string {
	return strings.TrimRight(base, "/") + "/" + strings.Join(segs, "/")
}

// missing lists the names whose values are absent (nil or empty string).
func missing(fields map[string]*string) []string {
	var out []string
	for name, v := range fields {
		if v == nil || *v == "" {
			out = append(out, name)
		}
	}
	return out
}

func asClientError(err error, target **client.ClientError) bool {
	return errors.As(err, target)
}
