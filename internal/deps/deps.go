// Package deps implements nugctl's dependency-graph scan: for every package
// version in a feed (or a single package with --package), it reads the
// registration's dependencyGroups and checks whether each declared
// dependency actually resolves against what the feed has listed.
package deps

import (
	"fmt"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
	"github.com/mwtrigg/nugctl/internal/nugetversion"
)

type Status string

const (
	StatusOK          Status = "ok"
	StatusMissing     Status = "missing"
	StatusUnsatisfied Status = "unsatisfied"
)

// Finding is one dependency edge's resolution result.
type Finding struct {
	Package         string `json:"package"`
	Version         string `json:"version"`
	TargetFramework string `json:"targetFramework,omitempty"`
	DependencyID    string `json:"dependencyId"`
	Range           string `json:"range"`
	Status          Status `json:"status"`
	Detail          string `json:"detail,omitempty"`
}

// Report is the full result of a dependency scan.
type Report struct {
	FeedURL     string    `json:"feedUrl"`
	Findings    []Finding `json:"findings"`
	Aborted     bool      `json:"aborted"`
	AbortReason string    `json:"abortReason,omitempty"`
}

// Add appends a finding to the report.
func (r *Report) Add(f Finding) {
	r.Findings = append(r.Findings, f)
}

// Counts returns the number of findings in each status, for summary lines.
func (r *Report) Counts() (ok, missing, unsatisfied int) {
	for _, f := range r.Findings {
		switch f.Status {
		case StatusOK:
			ok++
		case StatusMissing:
			missing++
		case StatusUnsatisfied:
			unsatisfied++
		}
	}
	return
}

// ExitCode implements the same CI exit contract as internal/verify.Report:
// 2 on abort, 1 if any dependency is missing or unsatisfied, 0 otherwise.
func (r *Report) ExitCode() int {
	if r.Aborted {
		return 2
	}
	for _, f := range r.Findings {
		if f.Status != StatusOK {
			return 1
		}
	}
	return 0
}

// Options controls which packages Run scans.
type Options struct {
	Package string // target package ID; "" = scan every package in the feed
}

// Run scans dependencyGroups across the target package(s) and returns the
// full report. It never returns an error — a fatal setup failure (e.g. the
// feed's package list or a target package's registration can't be fetched)
// is represented as Report.Aborted.
func Run(c *client.Client, opts Options) *Report {
	r := &Report{FeedURL: c.BaseURL}

	ids, err := packageIDs(c, opts)
	if err != nil {
		r.Aborted = true
		r.AbortReason = err.Error()
		return r
	}
	if len(ids) == 0 {
		return r
	}

	cache := newFeedCache(c)
	for _, id := range ids {
		idx, err := c.Registration(id)
		if err != nil {
			r.Aborted = true
			r.AbortReason = fmt.Sprintf("fetching registration for %s: %v", id, err)
			return r
		}
		entries, err := registrationEntries(c, idx)
		if err != nil {
			r.Aborted = true
			r.AbortReason = fmt.Sprintf("fetching registration pages for %s: %v", id, err)
			return r
		}
		cache.storeEntries(id, entries)

		for _, entry := range entries {
			if entry.Version == "" {
				continue
			}
			if err := scanEntry(cache, entry, r); err != nil {
				r.Aborted = true
				r.AbortReason = err.Error()
				return r
			}
		}
	}
	return r
}

// registrationEntries flattens idx into catalog entries, transparently
// fetching any page the feed left out-of-line (only "@id" and "count", per
// the NuGet v3 registration schema — see RegistrationPage.Inline) rather
// than inlining its leaves.
func registrationEntries(c *client.Client, idx *client.RegistrationIndex) ([]client.CatalogEntry, error) {
	var entries []client.CatalogEntry
	for _, page := range idx.Items {
		if page.Inline() {
			for _, leaf := range page.Items {
				entries = append(entries, leaf.CatalogEntry)
			}
			continue
		}
		if page.ID == "" {
			return nil, fmt.Errorf("registration page has no items and no @id to fetch")
		}
		full, err := c.RegistrationPageAt(page.ID)
		if err != nil {
			return nil, fmt.Errorf("fetching registration page %s: %w", page.ID, err)
		}
		for _, leaf := range full.Items {
			entries = append(entries, leaf.CatalogEntry)
		}
	}
	return entries, nil
}

func scanEntry(cache *feedCache, entry client.CatalogEntry, r *Report) error {
	for _, group := range entry.DependencyGroups {
		for _, dep := range group.Dependencies {
			f := Finding{
				Package:         entry.ID,
				Version:         entry.Version,
				TargetFramework: group.TargetFramework,
				DependencyID:    dep.ID,
				Range:           dep.Range,
			}

			versions, exists, err := cache.versionsOf(dep.ID)
			if err != nil {
				return fmt.Errorf("checking dependency %s of %s %s: %w", dep.ID, entry.ID, entry.Version, err)
			}
			if !exists {
				f.Status = StatusMissing
				f.Detail = "dependency package not found in feed"
				r.Add(f)
				continue
			}

			rng, err := nugetversion.ParseRange(dep.Range)
			if err != nil {
				f.Status = StatusUnsatisfied
				f.Detail = fmt.Sprintf("malformed version range: %v", err)
				r.Add(f)
				continue
			}

			if anySatisfies(versions, rng) {
				f.Status = StatusOK
			} else {
				f.Status = StatusUnsatisfied
				f.Detail = fmt.Sprintf("no listed version satisfies %s", dep.Range)
			}
			r.Add(f)
		}
	}
	return nil
}

// anySatisfies reports whether any of versions satisfies rng, excluding
// prerelease candidates unless rng itself references a prerelease bound —
// a plain stable range must not be satisfied by a prerelease version.
func anySatisfies(versions []nugetversion.Version, rng *nugetversion.Range) bool {
	allowPrerelease := rng.AllowsPrerelease()
	for _, v := range versions {
		if v.Prerelease != "" && !allowPrerelease {
			continue
		}
		if rng.Satisfies(v) {
			return true
		}
	}
	return false
}

// packageIDs returns the package IDs Run should walk: just opts.Package if
// set (its existence is confirmed by Run's own registration fetch), or
// every package ID discovered via a full, deduplicated search sweep.
func packageIDs(c *client.Client, opts Options) ([]string, error) {
	if opts.Package != "" {
		return []string{opts.Package}, nil
	}
	return discoverAllIDs(c)
}

// searchPageSize is how many hits discoverAllIDs requests per page while
// sweeping the feed's full package list.
const searchPageSize = 100

func discoverAllIDs(c *client.Client) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	skip := 0
	for {
		res, err := c.Search("", skip, searchPageSize, true)
		if err != nil {
			return nil, fmt.Errorf("listing packages: %w", err)
		}
		if len(res.Data) == 0 {
			break
		}
		for _, p := range res.Data {
			key := strings.ToLower(p.ID)
			if !seen[key] {
				seen[key] = true
				ids = append(ids, p.ID)
			}
		}
		skip += len(res.Data)
		if skip >= res.TotalHits || len(res.Data) < searchPageSize {
			break
		}
	}
	return ids, nil
}

// feedCache memoizes registration lookups so a package referenced as a
// dependency many times over (or that also appears in the top-level scan
// list) is only fetched from the feed once per run.
type feedCache struct {
	c        *client.Client
	exists   map[string]bool
	versions map[string][]nugetversion.Version
}

func newFeedCache(c *client.Client) *feedCache {
	return &feedCache{c: c, exists: map[string]bool{}, versions: map[string][]nugetversion.Version{}}
}

// storeEntries records an already-fetched, already-expanded registration,
// so Run's top-level walk doesn't cause versionsOf to re-fetch it when it's
// also a dependency.
func (fc *feedCache) storeEntries(id string, entries []client.CatalogEntry) {
	key := strings.ToLower(id)
	if _, ok := fc.exists[key]; ok {
		return
	}
	fc.set(key, entries)
}

// versionsOf returns every listed version of id and whether id exists in
// the feed at all (existence is independent of whether any version is
// listed, matching the deps command's MISSING-vs-UNSATISFIED distinction).
// A non-nil error means the lookup itself failed (network, auth, 5xx, a
// malformed out-of-line page, ...) — the caller must not treat that as
// "missing", since that would misreport a real failure as a clean result.
func (fc *feedCache) versionsOf(id string) (versions []nugetversion.Version, exists bool, err error) {
	key := strings.ToLower(id)
	if exists, ok := fc.exists[key]; ok {
		return fc.versions[key], exists, nil
	}
	idx, ferr := fc.c.Registration(id)
	if ferr != nil {
		if client.IsNotFound(ferr) {
			fc.exists[key] = false
			fc.versions[key] = nil
			return nil, false, nil
		}
		return nil, false, ferr
	}
	entries, ferr := registrationEntries(fc.c, idx)
	if ferr != nil {
		return nil, false, ferr
	}
	fc.set(key, entries)
	return fc.versions[key], fc.exists[key], nil
}

func (fc *feedCache) set(key string, entries []client.CatalogEntry) {
	fc.exists[key] = len(entries) > 0

	var listed []nugetversion.Version
	for _, e := range entries {
		if e.Version == "" || !e.Listed {
			continue
		}
		if v, err := nugetversion.Parse(e.Version); err == nil {
			listed = append(listed, v)
		}
	}
	fc.versions[key] = listed
}
