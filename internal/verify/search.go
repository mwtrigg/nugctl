package verify

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
)

// Search and autocomplete checks. Spec:
// https://learn.microsoft.com/nuget/api/search-query-service-resource and
// https://learn.microsoft.com/nuget/api/search-autocomplete-service-resource

// rawSearch is the search response with every spec-required property as a
// pointer, so an absent property is distinguishable from a zero value.
type rawSearch struct {
	TotalHits *int             `json:"totalHits"`
	Data      *[]rawSearchItem `json:"data"`
}

type rawSearchItem struct {
	ID       *string             `json:"id"`
	Version  *string             `json:"version"`
	Versions *[]rawSearchVersion `json:"versions"`
}

type rawSearchVersion struct {
	ID        *string `json:"@id"`
	Version   *string `json:"version"`
	Downloads *int64  `json:"downloads"`
}

// checkSearchShape asserts the search result object's required
// properties: totalHits and data at the root; id, version and versions[] on
// each result; @id, version and downloads on each versions[] entry. The
// result's own version is the latest of versions[], so it must be among
// them.
func checkSearchShape(c *client.Client, r *Report) {
	const name = "search result shape (versions[])"
	base, err := c.ResourceURL("SearchQueryService")
	if err != nil {
		return // "resource present" already failed
	}
	u := client.SearchURL(base, client.SearchParams{Take: 5, Prerelease: true, SemVerLevel: client.SemVerLevel2})
	var res rawSearch
	if _, err := getJSON(c, u, &res); err != nil {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: "GET " + u, Err: err.Error()})
		return
	}
	if problems := searchShapeProblems(&res); len(problems) > 0 {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: strings.Join(problems, "; ")})
		return
	}
	if len(*res.Data) == 0 {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusSkip, Detail: "search returned no results to inspect"})
		return
	}
	r.Add(Check{Name: name, Category: "readonly", Status: StatusPass, Detail: fmt.Sprintf("%d result(s)", len(*res.Data))})
}

func searchShapeProblems(res *rawSearch) []string {
	var p []string
	if res.TotalHits == nil {
		p = append(p, "root: missing totalHits")
	}
	if res.Data == nil {
		return append(p, "root: missing data")
	}
	for i, it := range *res.Data {
		where := fmt.Sprintf("data[%d]", i)
		if it.ID != nil && *it.ID != "" {
			where = fmt.Sprintf("data[%d] (%s)", i, *it.ID)
		}
		for _, f := range missing(map[string]*string{"id": it.ID, "version": it.Version}) {
			p = append(p, where+": missing "+f)
		}
		if it.Versions == nil {
			p = append(p, where+": missing versions")
			continue
		}
		latestListed := false
		for j, v := range *it.Versions {
			for _, f := range missing(map[string]*string{"@id": v.ID, "version": v.Version}) {
				p = append(p, fmt.Sprintf("%s.versions[%d]: missing %s", where, j, f))
			}
			if v.Downloads == nil {
				p = append(p, fmt.Sprintf("%s.versions[%d]: missing downloads", where, j))
			}
			if v.Version != nil && it.Version != nil && sameVersion(*v.Version, *it.Version) {
				latestListed = true
			}
		}
		if it.Version != nil && !latestListed {
			p = append(p, fmt.Sprintf("%s: version %s is not in versions[]", where, *it.Version))
		}
	}
	return p
}

// searchHas reports whether a search with p returns a result whose id
// equals id (ignoring case; casing is checked separately).
func searchHas(c *client.Client, p client.SearchParams, id string) (bool, error) {
	res, err := c.SearchWith(p)
	if err != nil {
		return false, err
	}
	for _, d := range res.Data {
		if strings.EqualFold(d.ID, id) {
			return true, nil
		}
	}
	return false, nil
}

// --- autocomplete ---

type rawAutocompleteIDs struct {
	TotalHits *int      `json:"totalHits"`
	Data      *[]string `json:"data"`
}

type rawAutocompleteVersions struct {
	Data *[]string `json:"data"`
}

func autocompleteBase(c *client.Client) string {
	base, err := c.ResourceURL("SearchAutocompleteService")
	if err != nil {
		return ""
	}
	return base
}

// autocompleteVersions enumerates id's versions through autocomplete's
// ?id= form. prerelease=false and an empty semVerLevel leave the parameter
// out, which the spec defines as "exclude".
func autocompleteVersions(c *client.Client, base, id string, prerelease bool, semVerLevel string) ([]string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("id", id)
	if prerelease {
		q.Set("prerelease", "true")
	}
	if semVerLevel != "" {
		q.Set("semVerLevel", semVerLevel)
	}
	u.RawQuery = q.Encode()
	var res rawAutocompleteVersions
	if _, err := getJSON(c, u.String(), &res); err != nil {
		return nil, err
	}
	if res.Data == nil {
		return nil, fmt.Errorf("GET %s: response has no data array", u)
	}
	return *res.Data, nil
}

func containsVersion(vs []string, v string) bool {
	for _, x := range vs {
		if sameVersion(x, v) {
			return true
		}
	}
	return false
}

// checkAutocomplete runs the readonly autocomplete checks when the feed
// advertises SearchAutocompleteService (an optional resource; absent means
// skip). Package-id search must return totalHits and a data array of
// strings. Version enumeration for one package must return a data array of
// strings, each a version the flat container (which lists every version,
// listed or not) knows.
func checkAutocomplete(c *client.Client, opts Options, r *Report) {
	const cat = "readonly"
	base := autocompleteBase(c)
	if base == "" {
		r.Add(Check{Name: "autocomplete: package ids", Category: cat, Status: StatusSkip, Detail: "SearchAutocompleteService not advertised (optional resource)"})
		return
	}
	u := client.SearchURL(base, client.SearchParams{Take: 5, Prerelease: true, SemVerLevel: client.SemVerLevel2})
	var ids rawAutocompleteIDs
	if _, err := getJSON(c, u, &ids); err != nil {
		r.Add(Check{Name: "autocomplete: package ids", Category: cat, Status: StatusFail, Detail: "GET " + u, Err: err.Error()})
		return
	}
	if ids.TotalHits == nil || ids.Data == nil {
		r.Add(Check{Name: "autocomplete: package ids", Category: cat, Status: StatusFail, Detail: "response lacks totalHits or data"})
		return
	}
	r.Add(Check{Name: "autocomplete: package ids", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("%d id(s)", len(*ids.Data))})

	id := opts.Package
	if id == "" && len(*ids.Data) > 0 {
		id = (*ids.Data)[0]
	}
	if id == "" {
		r.Add(Check{Name: "autocomplete: package versions", Category: cat, Status: StatusSkip, Detail: "no package to enumerate (pass --package)"})
		return
	}
	vs, err := autocompleteVersions(c, base, id, true, client.SemVerLevel2)
	if err != nil {
		r.Add(Check{Name: "autocomplete: package versions", Category: cat, Status: StatusFail, Detail: "id=" + id, Err: err.Error()})
		return
	}
	flat, err := c.FlatContainerVersions(id)
	if err != nil {
		r.Add(Check{Name: "autocomplete: package versions", Category: cat, Status: StatusSkip, Detail: "id=" + id + ": flat container list unavailable to compare against", Err: err.Error()})
		return
	}
	var unknown []string
	for _, v := range vs {
		if !containsVersion(flat.Versions, v) {
			unknown = append(unknown, v)
		}
	}
	if len(unknown) > 0 {
		r.Add(Check{Name: "autocomplete: package versions", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("id=%s: versions %v are not in the flat container %v", id, unknown, flat.Versions)})
		return
	}
	r.Add(Check{Name: "autocomplete: package versions", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s, %d version(s)", id, len(vs))})
}

// --- push-phase filter checks ---

// runSearchFilterChecks pushes a prerelease-only package (SemVer 1.0.0
// label) and a SemVer 2.0.0-only package and checks the spec's defaults:
// without prerelease=true, prerelease packages are excluded; without
// semVerLevel=2.0.0, only SemVer 1.0.0-compatible versions are returned.
// Both rules hold for search and for autocomplete when advertised. It
// returns what it pushed, for cleanup.
func runSearchFilterChecks(c *client.Client, r *Report, stamp int64) []pkgRef {
	const cat = "push"
	var pushed []pkgRef

	pre := pkgRef{ID: fmt.Sprintf("NugCtl.Verify.Pre-%d", stamp), Version: "1.0.0-beta"}
	if pushAnyField(c, r, pre) {
		pushed = append(pushed, pre)
		all := client.SearchParams{Query: pre.ID, Take: 20, Prerelease: true, SemVerLevel: client.SemVerLevel2}
		if !pollUntil(func() bool { ok, _ := searchHas(c, all, pre.ID); return ok }) {
			r.Add(Check{Name: "search excludes prerelease by default", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s %s never appeared even with prerelease=true after %s", pre.ID, pre.Version, pollTimeout)})
		} else {
			noPre := all
			noPre.Prerelease = false
			has, err := searchHas(c, noPre, pre.ID)
			switch {
			case err != nil:
				r.Add(Check{Name: "search excludes prerelease by default", Category: cat, Status: StatusFail, Err: err.Error()})
			case has:
				r.Add(Check{Name: "search excludes prerelease by default", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("prerelease-only %s returned without prerelease=true; the spec says pre-release packages are then excluded", pre.ID)})
			default:
				r.Add(Check{Name: "search excludes prerelease by default", Category: cat, Status: StatusPass})
			}
			autocompleteFilterCheck(c, r, "autocomplete excludes prerelease by default", pre, false, client.SemVerLevel2)
		}
	}

	sv2 := pkgRef{ID: fmt.Sprintf("NugCtl.Verify.SemVer2-%d", stamp), Version: "1.0.0-rc.1"}
	if pushExtra(c, r, "push SemVer 2.0.0 package", sv2, nil, "package") {
		pushed = append(pushed, sv2)
		all := client.SearchParams{Query: sv2.ID, Take: 20, Prerelease: true, SemVerLevel: client.SemVerLevel2}
		if !pollUntil(func() bool { ok, _ := searchHas(c, all, sv2.ID); return ok }) {
			r.Add(Check{Name: "search excludes SemVer 2.0.0 without semVerLevel", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s %s never appeared even with semVerLevel=2.0.0 after %s", sv2.ID, sv2.Version, pollTimeout)})
		} else {
			noSV2 := all
			noSV2.SemVerLevel = ""
			has, err := searchHas(c, noSV2, sv2.ID)
			switch {
			case err != nil:
				r.Add(Check{Name: "search excludes SemVer 2.0.0 without semVerLevel", Category: cat, Status: StatusFail, Err: err.Error()})
			case has:
				r.Add(Check{Name: "search excludes SemVer 2.0.0 without semVerLevel", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("SemVer 2.0.0-only %s (%s) returned without semVerLevel=2.0.0", sv2.ID, sv2.Version)})
			default:
				r.Add(Check{Name: "search excludes SemVer 2.0.0 without semVerLevel", Category: cat, Status: StatusPass})
			}
			autocompleteFilterCheck(c, r, "autocomplete excludes SemVer 2.0.0 without semVerLevel", sv2, true, "")
		}
	}
	return pushed
}

// autocompleteFilterCheck enumerates p's versions through autocomplete
// twice: with everything opted in (p's version must be there) and with the
// given filter (it must not be). Skips when autocomplete isn't advertised.
func autocompleteFilterCheck(c *client.Client, r *Report, name string, p pkgRef, prerelease bool, semVerLevel string) {
	const cat = "push"
	base := autocompleteBase(c)
	if base == "" {
		r.Add(Check{Name: name, Category: cat, Status: StatusSkip, Detail: "SearchAutocompleteService not advertised (optional resource)"})
		return
	}
	var all []string
	var err error
	if !pollUntil(func() bool {
		all, err = autocompleteVersions(c, base, p.ID, true, client.SemVerLevel2)
		return err == nil && containsVersion(all, p.Version)
	}) {
		detail := fmt.Sprintf("%s %s not enumerated even with prerelease=true&semVerLevel=2.0.0 (got %v)", p.ID, p.Version, all)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		r.Add(Check{Name: name, Category: cat, Status: StatusFail, Detail: detail, Err: errStr})
		return
	}
	filtered, err := autocompleteVersions(c, base, p.ID, prerelease, semVerLevel)
	switch {
	case err != nil:
		r.Add(Check{Name: name, Category: cat, Status: StatusFail, Err: err.Error()})
	case containsVersion(filtered, p.Version):
		r.Add(Check{Name: name, Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s %s enumerated without opting in: %v", p.ID, p.Version, filtered)})
	default:
		r.Add(Check{Name: name, Category: cat, Status: StatusPass})
	}
}
