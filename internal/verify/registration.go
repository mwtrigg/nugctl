package verify

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/mwtrigg/nugctl/internal/nugetversion"
)

// Registration (package metadata) checks. Spec:
// https://learn.microsoft.com/nuget/api/registration-base-url-resource

// Raw registration shapes: every property the spec marks required is a
// pointer, so "absent" is distinguishable from a zero value.
type regIndexRaw struct {
	Count *int          `json:"count"`
	Items *[]regPageRaw `json:"items"`
}

type regPageRaw struct {
	ID     *string       `json:"@id"`
	Count  *int          `json:"count"`
	Items  *[]regLeafRaw `json:"items"`
	Lower  *string       `json:"lower"`
	Upper  *string       `json:"upper"`
	Parent *string       `json:"parent"`
}

type regLeafRaw struct {
	ID             *string         `json:"@id"`
	PackageContent *string         `json:"packageContent"`
	CatalogEntry   json.RawMessage `json:"catalogEntry"`
}

type catalogEntryRaw struct {
	AtID    *string `json:"@id"`
	ID      *string `json:"id"`
	Version *string `json:"version"`
}

// registration is a registration index with every out-of-line page fetched
// and its leaves filled in, plus whatever required properties were missing.
type registration struct {
	Index     client.RegistrationIndex
	Problems  []string
	OutOfLine int // pages fetched separately
}

func registrationIndexURL(c *client.Client, id string) (string, error) {
	base, err := c.ResourceURL("RegistrationsBaseUrl")
	if err != nil {
		return "", err
	}
	return joinURL(base, strings.ToLower(id), "index.json"), nil
}

// loadRegistration fetches id's registration index and any page the feed
// left out of line ("If the items property is not present in the
// registration page object, the URL specified in the @id must be used"),
// checking the required properties of the index, each page and each leaf.
func loadRegistration(c *client.Client, id string) (*registration, error) {
	u, err := registrationIndexURL(c, id)
	if err != nil {
		return nil, err
	}
	reg := &registration{}
	var raw regIndexRaw
	body, err := getJSON(c, u, &raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &reg.Index); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", u, err)
	}
	if raw.Count == nil {
		reg.Problems = append(reg.Problems, "index: missing count")
	}
	if raw.Items == nil {
		reg.Problems = append(reg.Problems, "index: missing items")
		return reg, nil
	}
	if raw.Count != nil && *raw.Count != len(*raw.Items) {
		reg.Problems = append(reg.Problems, fmt.Sprintf("index: count %d but %d page(s)", *raw.Count, len(*raw.Items)))
	}
	for i, page := range *raw.Items {
		where := fmt.Sprintf("page[%d]", i)
		reg.Problems = append(reg.Problems, pageProblems(where, page, false)...)
		if page.Items != nil {
			continue
		}
		// Out of line: the page document must be fetched from its @id.
		if page.ID == nil || *page.ID == "" {
			continue // already reported; nothing to fetch
		}
		reg.OutOfLine++
		var fetchedRaw regPageRaw
		pb, err := getJSON(c, *page.ID, &fetchedRaw)
		if err != nil {
			reg.Problems = append(reg.Problems, fmt.Sprintf("%s: fetching out-of-line page %s: %v", where, *page.ID, err))
			continue
		}
		reg.Problems = append(reg.Problems, pageProblems(where+" (fetched)", fetchedRaw, true)...)
		var fetched client.RegistrationPage
		if err := json.Unmarshal(pb, &fetched); err != nil {
			reg.Problems = append(reg.Problems, fmt.Sprintf("%s: decoding %s: %v", where, *page.ID, err))
			continue
		}
		if i < len(reg.Index.Items) {
			reg.Index.Items[i].Items = fetched.Items
		}
	}
	return reg, nil
}

// pageProblems checks one registration page object. In the index, items
// and parent are optional; a separately fetched page requires both.
func pageProblems(where string, p regPageRaw, fetched bool) []string {
	var out []string
	for _, f := range missing(map[string]*string{"@id": p.ID, "lower": p.Lower, "upper": p.Upper}) {
		out = append(out, where+": missing "+f)
	}
	if p.Count == nil {
		out = append(out, where+": missing count")
	}
	if fetched {
		if p.Parent == nil || *p.Parent == "" {
			out = append(out, where+": missing parent")
		}
		if p.Items == nil {
			out = append(out, where+": missing items")
		}
	}
	if p.Items == nil {
		return out
	}
	if p.Count != nil && *p.Count != len(*p.Items) {
		out = append(out, fmt.Sprintf("%s: count %d but %d leaf/leaves", where, *p.Count, len(*p.Items)))
	}
	for j, leaf := range *p.Items {
		lw := fmt.Sprintf("%s.items[%d]", where, j)
		for _, f := range missing(map[string]*string{"@id": leaf.ID, "packageContent": leaf.PackageContent}) {
			out = append(out, lw+": missing "+f)
		}
		ce := bytes.TrimSpace(leaf.CatalogEntry)
		if len(ce) == 0 || ce[0] != '{' {
			out = append(out, lw+": catalogEntry is not an object")
			continue
		}
		var entry catalogEntryRaw
		if err := json.Unmarshal(ce, &entry); err != nil {
			out = append(out, lw+": catalogEntry: "+err.Error())
			continue
		}
		for _, f := range missing(map[string]*string{"@id": entry.AtID, "id": entry.ID, "version": entry.Version}) {
			out = append(out, lw+".catalogEntry: missing "+f)
		}
	}
	return out
}

// checkRegistrationFields reports reg's missing required properties.
func checkRegistrationFields(r *Report, id string, reg *registration) {
	const name = "registration required fields"
	detail := fmt.Sprintf("id=%s, %d page(s)", id, len(reg.Index.Items))
	if reg.OutOfLine > 0 {
		detail += fmt.Sprintf(", %d fetched out of line", reg.OutOfLine)
	}
	if len(reg.Problems) == 0 {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusPass, Detail: detail})
		return
	}
	shown := reg.Problems
	if len(shown) > 8 {
		shown = append(shown[:8:8], fmt.Sprintf("... %d more", len(reg.Problems)-8))
	}
	r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: detail + ": " + strings.Join(shown, "; ")})
}

// checkRegistrationGzip requests id's registration index from each
// advertised RegistrationsBaseUrl/3.4.0 and /3.6.0 hive with
// Accept-Encoding: gzip. The spec says those hives "are compressed using
// Content-Encoding: gzip". A feed that answers uncompressed still works
// with every client, so that is a warning; a non-200 or a body that does
// not decode is a failure.
func checkRegistrationGzip(c *client.Client, r *Report, id string) {
	idx, err := c.ServiceIndex()
	if err != nil {
		return
	}
	for _, t := range []string{"RegistrationsBaseUrl/3.4.0", "RegistrationsBaseUrl/3.6.0"} {
		var base string
		for _, res := range idx.Resources {
			if res.Type == t {
				base = res.ID
				break
			}
		}
		if base == "" {
			continue
		}
		name := "registration gzip: " + t
		u := joinURL(base, strings.ToLower(id), "index.json")
		resp, err := c.Raw(http.MethodGet, u, http.Header{"Accept-Encoding": {"gzip"}}, nil)
		if err != nil {
			r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: "GET " + u, Err: err.Error()})
			continue
		}
		if resp.StatusCode != http.StatusOK {
			r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: "GET " + u, Err: client.HTTPError(resp, u).Error()})
			continue
		}
		body := resp.Body
		gz := strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip")
		if gz {
			zr, err := gzip.NewReader(bytes.NewReader(body))
			if err == nil {
				body, err = io.ReadAll(zr)
			}
			if err != nil {
				r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: "Content-Encoding: gzip but the body does not gunzip", Err: err.Error()})
				continue
			}
		}
		if !json.Valid(body) {
			r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: "response body is not JSON"})
			continue
		}
		if !gz {
			r.Add(Check{Name: name, Category: "readonly", Status: StatusWarn, Detail: fmt.Sprintf("Accept-Encoding: gzip answered with Content-Encoding %q; the spec says this hive is gzipped", resp.Header.Get("Content-Encoding"))})
			continue
		}
		r.Add(Check{Name: name, Category: "readonly", Status: StatusPass})
	}
}

// findEntry returns version's catalog entry in reg, or nil.
func (reg *registration) findEntry(version string) *client.CatalogEntry {
	for _, page := range reg.Index.Items {
		for i := range page.Items {
			if sameVersion(page.Items[i].CatalogEntry.Version, version) {
				return &page.Items[i].CatalogEntry
			}
		}
	}
	return nil
}

// checkUnlistedInRegistration runs after a feed unlisted (not deleted)
// id/version. Registration lists every version ("listed: Should be
// considered as listed if absent"), so the unlisted one must still be in
// the index, with listed:false.
func checkUnlistedInRegistration(c *client.Client, r *Report, id, version string) {
	const name = "unlisted version stays in registration (listed:false)"
	var entry *client.CatalogEntry
	var lastErr error
	ok := pollUntil(func() bool {
		reg, err := loadRegistration(c, id)
		lastErr = err
		if err != nil {
			return false
		}
		entry = reg.findEntry(version)
		return entry != nil && !entry.Listed
	})
	switch {
	case ok:
		r.Add(Check{Name: name, Category: "push", Status: StatusPass})
	case entry != nil:
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s %s still listed:true in registration after %s", id, version, pollTimeout)})
	case lastErr != nil:
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("registration for %s unavailable after unlist", id), Err: lastErr.Error()})
	default:
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s %s dropped from the registration index after unlist", id, version)})
	}
}

// metaDep is the dependency the push checks' metadata package declares.
var metaDep = nuspecDep{ID: "NugCtl.Verify.Dep", Range: "[1.0.0, 2.0.0)"}

// runRegistrationBodyChecks inspects the registration and search entries
// of the pushed package meta (a mixed-case id with one dependency):
//   - catalogEntry.id and search data[].id must be the id exactly as in the
//     .nuspec. A feed that lowercases them breaks clients that save files
//     under the id they read (PowerShell's PSResourceGet on case-sensitive
//     filesystems).
//   - catalogEntry.dependencyGroups[].dependencies[] must carry the declared
//     dependency: id (required), range (optional in the schema, so absent
//     warns, but a different range fails) and registration (optional; absent
//     warns).
func runRegistrationBodyChecks(c *client.Client, r *Report, meta pkgRef) {
	const cat = "push"
	norm := normalizeVersion(meta.Version)
	var entry *client.CatalogEntry
	var lastErr error
	if !pollUntil(func() bool {
		reg, err := loadRegistration(c, meta.ID)
		lastErr = err
		if err == nil {
			entry = reg.findEntry(norm)
		}
		return entry != nil
	}) {
		detail := fmt.Sprintf("%s %s not in its registration index after %s", meta.ID, norm, pollTimeout)
		errStr := ""
		if lastErr != nil {
			errStr = lastErr.Error()
		}
		r.Add(Check{Name: "original id casing: registration", Category: cat, Status: StatusFail, Detail: detail, Err: errStr})
		r.Add(Check{Name: "registration dependencies", Category: cat, Status: StatusSkip, Detail: "no registration entry"})
	} else {
		if entry.ID == meta.ID {
			r.Add(Check{Name: "original id casing: registration", Category: cat, Status: StatusPass, Detail: entry.ID})
		} else {
			r.Add(Check{Name: "original id casing: registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("catalogEntry.id = %q, nuspec id = %q", entry.ID, meta.ID)})
		}
		checkDependencies(r, entry)
	}

	res, err := c.SearchWith(client.SearchParams{Query: meta.ID, Take: 20, Prerelease: true, SemVerLevel: client.SemVerLevel2})
	if err != nil {
		r.Add(Check{Name: "original id casing: search", Category: cat, Status: StatusFail, Err: err.Error()})
		return
	}
	for _, d := range res.Data {
		if !strings.EqualFold(d.ID, meta.ID) {
			continue
		}
		if d.ID == meta.ID {
			r.Add(Check{Name: "original id casing: search", Category: cat, Status: StatusPass, Detail: d.ID})
		} else {
			r.Add(Check{Name: "original id casing: search", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("data[].id = %q, nuspec id = %q", d.ID, meta.ID)})
		}
		return
	}
	r.Add(Check{Name: "original id casing: search", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s not found by search", meta.ID)})
}

func checkDependencies(r *Report, entry *client.CatalogEntry) {
	const name = "registration dependencies"
	var dep *client.Dependency
	for _, g := range entry.DependencyGroups {
		for i := range g.Dependencies {
			if strings.EqualFold(g.Dependencies[i].ID, metaDep.ID) {
				dep = &g.Dependencies[i]
			}
		}
	}
	if dep == nil {
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("declared dependency %s %s missing from catalogEntry.dependencyGroups (got %d group(s)); clients would install no dependencies", metaDep.ID, metaDep.Range, len(entry.DependencyGroups))})
		return
	}
	if dep.Range == "" {
		r.Add(Check{Name: name, Category: "push", Status: StatusWarn, Detail: fmt.Sprintf("%s present but without range; clients then assume (, ) instead of %s", dep.ID, metaDep.Range)})
		return
	}
	if !sameRange(dep.Range, metaDep.Range) {
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s range = %q, declared %q", dep.ID, dep.Range, metaDep.Range)})
		return
	}
	if dep.Registration == "" {
		r.Add(Check{Name: name, Category: "push", Status: StatusWarn, Detail: fmt.Sprintf("%s %s present, without the optional registration URL", dep.ID, dep.Range)})
		return
	}
	r.Add(Check{Name: name, Category: "push", Status: StatusPass, Detail: fmt.Sprintf("%s %s", dep.ID, dep.Range)})
}

// sameRange reports whether two NuGet version ranges allow the same
// versions, so "[1.0.0, 2.0.0)" equals "[1.0.0,2.0.0)" and "[1.0, 2.0)".
func sameRange(a, b string) bool {
	ra, err1 := nugetversion.ParseRange(a)
	rb, err2 := nugetversion.ParseRange(b)
	if err1 != nil || err2 != nil {
		return strings.EqualFold(strings.ReplaceAll(a, " ", ""), strings.ReplaceAll(b, " ", ""))
	}
	sameBound := func(x, y *nugetversion.Version) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return nugetversion.Compare(*x, *y) == 0
	}
	return sameBound(ra.MinVersion, rb.MinVersion) && sameBound(ra.MaxVersion, rb.MaxVersion) &&
		(ra.MinVersion == nil || ra.MinInclusive == rb.MinInclusive) &&
		(ra.MaxVersion == nil || ra.MaxInclusive == rb.MaxInclusive)
}
