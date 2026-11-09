package verify

import (
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mwtrigg/nugctl/internal/nugetversion"
)

// fakeFeed is a stateful in-memory NuGet v3 feed that follows the published
// spec (learn.microsoft.com/nuget/api), with switches that each break one
// rule so tests can show the matching check catches it.
type fakeFeed struct {
	mu         sync.Mutex
	pkgs       map[string]*fakePkg // lower(id)+"/"+lower(normalized version)
	downloaded map[string]bool
	deletes    []string // "lowerid/version?rawquery" of every DELETE received
	relists    int      // successful POST relists

	hardDelete       bool // DELETE removes the package instead of unlisting it
	guardRecentDL    bool // mimic Barn: 409 deleting a just-downloaded package unless force=true
	rejectAllDeletes bool // every DELETE 409s regardless of force
	noRelist         bool // POST relist answers 405

	// Spec violations.
	lowercaseIDs        bool // registration and search report ids lowercased
	ignorePrerelease    bool // prerelease versions returned without prerelease=true
	ignoreSemVerLevel   bool // SemVer 2.0.0 versions returned without semVerLevel=2.0.0
	requirePackageField bool // push only accepts the multipart field "package"
	noGzip              bool // registration never gzips
	dropDeps            bool // catalogEntry carries no dependencyGroups
	dropUnlisted        bool // unlisted versions vanish from registration and flat container
	outOfLinePages      bool // registration index pages carry no items (allowed by the spec)
	omitPageBounds      bool // pages lack lower/upper
	noAutocomplete      bool // SearchAutocompleteService not advertised
	allowOverwrite      bool // re-push of an existing version replaces it (2xx) instead of 409
	relistAbsentOK      bool // POST relist of an absent version answers 200
	keepRawVersions     bool // versions stored and reported as pushed, not normalized
	rewriteNuspec       bool // .nuspec download is re-serialized, not the packaged bytes
	noSearchVersions    bool // search results lack versions[]
}

type fakePkg struct {
	id, version string // id as pushed; version normalized (unless keepRawVersions)
	nupkg       []byte
	nuspec      []byte
	listed      bool
	deps        []nuspecDep
}

func newFakeFeed(hardDelete bool) *fakeFeed {
	return &fakeFeed{pkgs: map[string]*fakePkg{}, downloaded: map[string]bool{}, hardDelete: hardDelete}
}

func fkey(id, version string) string {
	return strings.ToLower(id) + "/" + strings.ToLower(normalizeVersion(version))
}

// mainDeletes returns the DELETEs aimed at the basic round trip's package
// (id "nugctl-verify-<stamp>"), leaving out the extra checks' packages.
func (f *fakeFeed) mainDeletes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, d := range f.deletes {
		if strings.HasPrefix(d, "nugctl-verify-") {
			out = append(out, d)
		}
	}
	return out
}

func (f *fakeFeed) shownID(id string) string {
	if f.lowercaseIDs {
		return strings.ToLower(id)
	}
	return id
}

// versionsOf returns id's packages sorted by version, optionally only listed.
func (f *fakeFeed) versionsOf(id string, onlyListed bool) []*fakePkg {
	var out []*fakePkg
	for _, p := range f.pkgs {
		if strings.EqualFold(p.id, id) && (p.listed || !onlyListed) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := nugetversion.Parse(out[i].version)
		b, _ := nugetversion.Parse(out[j].version)
		return nugetversion.Compare(a, b) < 0
	})
	return out
}

func (f *fakeFeed) filterVersion(v string, prerelease bool, semVerLevel string) bool {
	if strings.Contains(v, "-") && !prerelease && !f.ignorePrerelease {
		return false
	}
	if isSemVer2(v) && semVerLevel != "2.0.0" && !f.ignoreSemVerLevel {
		return false
	}
	return true
}

func writeMaybeGzip(w http.ResponseWriter, r *http.Request, body []byte, allowGzip bool) {
	w.Header().Set("Content-Type", "application/json")
	if allowGzip && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		gz.Write(body)
		gz.Close()
		return
	}
	w.Write(body)
}

func (f *fakeFeed) server() *httptest.Server {
	mux := http.NewServeMux()
	var srv *httptest.Server

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		base := srv.URL
		ac := ""
		if !f.noAutocomplete {
			ac = fmt.Sprintf(`,{"@id":"%s/autocomplete","@type":"SearchAutocompleteService/3.0.0-beta"}`, base)
		}
		fmt.Fprintf(w, `{"version":"3.0.0","resources":[
			{"@id":"%s/search","@type":"SearchQueryService/3.4.0"},
			{"@id":"%s/registration/","@type":"RegistrationsBaseUrl/3.6.0"},
			{"@id":"%s/flatcontainer/","@type":"PackageBaseAddress/3.0.0"},
			{"@id":"%s/publish","@type":"PackagePublish/2.0.0"}%s
		]}`, base, base, base, base, ac)
	})

	mux.HandleFunc("/publish", func(w http.ResponseWriter, r *http.Request) {
		mr, err := r.MultipartReader()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if f.requirePackageField && part.FormName() != "package" {
			http.Error(w, "missing form field 'package'", http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(part)
		nuspec, err := nuspecFromNupkg(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		doc, err := parseNuspec(nuspec)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		p := &fakePkg{id: doc.Metadata.ID, version: normalizeVersion(doc.Metadata.Version), nupkg: data, nuspec: nuspec, listed: true}
		if f.keepRawVersions {
			p.version = doc.Metadata.Version
		}
		for _, g := range doc.Metadata.Dependencies.Groups {
			for _, d := range g.Deps {
				p.deps = append(p.deps, nuspecDep{ID: d.ID, Range: d.Version})
			}
		}
		key := fkey(p.id, p.version)
		if f.keepRawVersions {
			key = strings.ToLower(p.id) + "/" + strings.ToLower(p.version)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, exists := f.pkgs[key]; exists && !f.allowOverwrite {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		f.pkgs[key] = p
		w.WriteHeader(http.StatusCreated)
	})

	mux.HandleFunc("/publish/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/publish/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		key := fkey(parts[0], parts[1])
		f.mu.Lock()
		defer f.mu.Unlock()
		p, ok := f.pkgs[key]
		if r.Method == http.MethodPost && !f.noRelist {
			if !ok && !f.relistAbsentOK {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			if ok {
				p.listed = true
				f.relists++
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f.deletes = append(f.deletes, key+"?"+r.URL.RawQuery)
		if f.rejectAllDeletes || (f.guardRecentDL && f.downloaded[key] && r.URL.Query().Get("force") != "true") {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprint(w, `{"type":"https://example.com/probs/recent-download","title":"package was recently downloaded","status":409}`)
			return
		}
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if f.hardDelete {
			delete(f.pkgs, key)
		} else {
			p.listed = false
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		q := strings.ToLower(r.URL.Query().Get("q"))
		pre := r.URL.Query().Get("prerelease") == "true"
		svl := r.URL.Query().Get("semVerLevel")
		f.mu.Lock()
		defer f.mu.Unlock()
		ids := map[string]string{}
		for _, p := range f.pkgs {
			ids[strings.ToLower(p.id)] = p.id
		}
		data := []map[string]any{}
		for lower, id := range ids {
			if q != "" && !strings.Contains(lower, q) {
				continue
			}
			var vs []map[string]any
			latest := ""
			for _, p := range f.versionsOf(id, true) {
				if !f.filterVersion(p.version, pre, svl) {
					continue
				}
				vs = append(vs, map[string]any{"@id": srv.URL + "/registration/" + lower + "/" + strings.ToLower(p.version) + ".json", "version": p.version, "downloads": 0})
				latest = p.version
			}
			if len(vs) == 0 {
				continue
			}
			item := map[string]any{"id": f.shownID(id), "version": latest, "versions": vs}
			if f.noSearchVersions {
				delete(item, "versions")
			}
			data = append(data, item)
		}
		json.NewEncoder(w).Encode(map[string]any{"totalHits": len(data), "data": data})
	})

	mux.HandleFunc("/autocomplete", func(w http.ResponseWriter, r *http.Request) {
		qs := r.URL.Query()
		pre := qs.Get("prerelease") == "true"
		svl := qs.Get("semVerLevel")
		f.mu.Lock()
		defer f.mu.Unlock()
		if id := qs.Get("id"); id != "" {
			out := []string{}
			for _, p := range f.versionsOf(id, true) {
				if f.filterVersion(p.version, pre, svl) {
					out = append(out, p.version)
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": out})
			return
		}
		seen := map[string]bool{}
		out := []string{}
		for _, p := range f.pkgs {
			if p.listed && !seen[strings.ToLower(p.id)] && f.filterVersion(p.version, pre, svl) {
				seen[strings.ToLower(p.id)] = true
				out = append(out, p.id)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"totalHits": len(out), "data": out})
	})

	mux.HandleFunc("/registration/", func(w http.ResponseWriter, r *http.Request) {
		// {lowerid}/index.json | {lowerid}/page/1.json | {lowerid}/{version}.json
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/registration/"), "/")
		lower := parts[0]
		regBase := srv.URL + "/registration/" + lower
		f.mu.Lock()
		defer f.mu.Unlock()
		var vs []*fakePkg
		for _, p := range f.versionsOf(lower, false) {
			if p.listed || !f.dropUnlisted {
				vs = append(vs, p)
			}
		}
		if len(vs) == 0 {
			http.NotFound(w, r)
			return
		}
		leaves := make([]map[string]any, 0, len(vs))
		for _, p := range vs {
			sum := sha512.Sum512(p.nupkg)
			ce := map[string]any{
				"@id": srv.URL + "/catalog/" + lower + "." + strings.ToLower(p.version) + ".json",
				"id":  f.shownID(p.id), "version": p.version, "listed": p.listed,
				"packageHash": base64.StdEncoding.EncodeToString(sum[:]), "packageHashAlgorithm": "SHA512",
			}
			if len(p.deps) > 0 && !f.dropDeps {
				var deps []map[string]any
				for _, d := range p.deps {
					deps = append(deps, map[string]any{"id": d.ID, "range": d.Range, "registration": srv.URL + "/registration/" + strings.ToLower(d.ID) + "/index.json"})
				}
				ce["dependencyGroups"] = []map[string]any{{"dependencies": deps}}
			}
			leaves = append(leaves, map[string]any{
				"@id":            regBase + "/" + strings.ToLower(p.version) + ".json",
				"packageContent": fmt.Sprintf("%s/flatcontainer/%s/%s/%s.%s.nupkg", srv.URL, lower, strings.ToLower(p.version), lower, strings.ToLower(p.version)),
				"catalogEntry":   ce,
			})
		}
		page := map[string]any{"@id": regBase + "/page/1.json", "count": len(leaves), "lower": vs[0].version, "upper": vs[len(vs)-1].version}
		if f.omitPageBounds {
			delete(page, "lower")
			delete(page, "upper")
		}
		var body any
		switch {
		case len(parts) == 2 && parts[1] == "index.json":
			if !f.outOfLinePages {
				page["items"] = leaves
				page["parent"] = regBase + "/index.json"
			}
			body = map[string]any{"count": 1, "items": []any{page}}
		case len(parts) == 3 && parts[1] == "page":
			page["items"] = leaves
			page["parent"] = regBase + "/index.json"
			body = page
		case len(parts) == 2:
			v := strings.TrimSuffix(parts[1], ".json")
			var hit *fakePkg
			for _, p := range vs {
				if strings.EqualFold(p.version, v) {
					hit = p
				}
			}
			if hit == nil {
				http.NotFound(w, r)
				return
			}
			body = map[string]any{"@id": regBase + "/" + v + ".json", "catalogEntry": srv.URL + "/catalog/" + lower + "." + v + ".json", "listed": hit.listed, "registration": regBase + "/index.json"}
		default:
			http.NotFound(w, r)
			return
		}
		b, _ := json.Marshal(body)
		writeMaybeGzip(w, r, b, !f.noGzip)
	})

	mux.HandleFunc("/flatcontainer/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/flatcontainer/"), "/")
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(parts) == 2 && parts[1] == "index.json" {
			out := []string{}
			for _, p := range f.versionsOf(parts[0], false) {
				if p.listed || !f.dropUnlisted {
					out = append(out, strings.ToLower(p.version))
				}
			}
			if len(out) == 0 {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"versions": out})
			return
		}
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		key := parts[0] + "/" + parts[1]
		if !f.keepRawVersions {
			key = fkey(parts[0], parts[1])
		}
		p, ok := f.pkgs[key]
		if !ok || (!p.listed && f.dropUnlisted) {
			http.NotFound(w, r)
			return
		}
		switch parts[2] {
		case parts[0] + ".nuspec":
			if f.rewriteNuspec {
				fmt.Fprintf(w, `<package><metadata><id>%s</id><version>%s</version></metadata></package>`, p.id, p.version)
				return
			}
			w.Write(p.nuspec)
		case parts[0] + "." + parts[1] + ".nupkg":
			f.downloaded[key] = true
			w.Write(p.nupkg)
		default:
			http.NotFound(w, r)
		}
	})

	srv = httptest.NewServer(mux)
	return srv
}

// fastPolls shortens the push checks' poll timeout for one test.
func fastPolls(t *testing.T) {
	old := pollTimeout
	pollTimeout = 3 * time.Second
	t.Cleanup(func() { pollTimeout = old })
}
