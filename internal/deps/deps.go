// Package deps implements nugctl's dependency-graph scan: for every package
// version in a feed (or a single package with --package), it reads the
// registration's dependencyGroups and checks whether each declared
// dependency actually resolves against what the feed has listed.
package deps

import (
	"fmt"
	"slices"
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
		cache.store(id, idx)

		for _, page := range idx.Items {
			for _, leaf := range page.Items {
				entry := leaf.CatalogEntry
				if entry.Version == "" {
					continue
				}
				scanEntry(cache, entry, r)
			}
		}
	}
	return r
}

func scanEntry(cache *feedCache, entry client.CatalogEntry, r *Report) {
	for _, group := range entry.DependencyGroups {
		for _, dep := range group.Dependencies {
			f := Finding{
				Package:         entry.ID,
				Version:         entry.Version,
				TargetFramework: group.TargetFramework,
				DependencyID:    dep.ID,
				Range:           dep.Range,
			}

			versions, exists := cache.versionsOf(dep.ID)
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

			satisfied := slices.ContainsFunc(versions, rng.Satisfies)
			if satisfied {
				f.Status = StatusOK
			} else {
				f.Status = StatusUnsatisfied
				f.Detail = fmt.Sprintf("no listed version satisfies %s", dep.Range)
			}
			r.Add(f)
		}
	}
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

// store records an already-fetched registration index, so Run's top-level
// walk doesn't cause versionsOf to re-fetch it when it's also a dependency.
func (fc *feedCache) store(id string, idx *client.RegistrationIndex) {
	key := strings.ToLower(id)
	if _, ok := fc.exists[key]; ok {
		return
	}
	fc.set(key, idx)
}

// versionsOf returns every listed version of id and whether id exists in
// the feed at all (existence is independent of whether any version is
// listed, matching the deps command's MISSING-vs-UNSATISFIED distinction).
func (fc *feedCache) versionsOf(id string) (versions []nugetversion.Version, exists bool) {
	key := strings.ToLower(id)
	if exists, ok := fc.exists[key]; ok {
		return fc.versions[key], exists
	}
	idx, err := fc.c.Registration(id)
	if err != nil {
		fc.exists[key] = false
		return nil, false
	}
	fc.set(key, idx)
	return fc.versions[key], fc.exists[key]
}

func (fc *feedCache) set(key string, idx *client.RegistrationIndex) {
	var entries []client.CatalogEntry
	for _, page := range idx.Items {
		for _, leaf := range page.Items {
			entries = append(entries, leaf.CatalogEntry)
		}
	}
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
