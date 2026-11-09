package verify

import (
	"bytes"
	"crypto/sha512"
	"fmt"
	"net/http"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
)

// Flat container (PackageBaseAddress/3.0.0) checks. Spec:
// https://learn.microsoft.com/nuget/api/package-base-address-resource
// Version normalization rules:
// https://learn.microsoft.com/nuget/concepts/package-versioning#normalized-version-numbers

// fetchNuspec GETs {@id}/{LOWER_ID}/{LOWER_VERSION}/{LOWER_ID}.nuspec.
func fetchNuspec(c *client.Client, id, version string) ([]byte, error) {
	base, err := c.ResourceURL("PackageBaseAddress")
	if err != nil {
		return nil, err
	}
	lid, lv := strings.ToLower(id), strings.ToLower(normalizeVersion(version))
	u := joinURL(base, lid, lv, lid+".nuspec")
	resp, err := c.Raw(http.MethodGet, u, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, client.HTTPError(resp, u)
	}
	return resp.Body, nil
}

// checkNuspecDownload (readonly) fetches the .nuspec for one version of id
// and checks it is the manifest of that package: XML whose metadata id and
// version match.
func checkNuspecDownload(c *client.Client, r *Report, id, version string) {
	const name = "flat container .nuspec download"
	b, err := fetchNuspec(c, id, version)
	if err != nil {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: fmt.Sprintf("id=%s version=%s", id, version), Err: err.Error()})
		return
	}
	doc, err := parseNuspec(b)
	if err != nil {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: fmt.Sprintf("id=%s version=%s: response is not a nuspec", id, version), Err: err.Error()})
		return
	}
	if !strings.EqualFold(doc.Metadata.ID, id) || !sameVersion(doc.Metadata.Version, version) {
		r.Add(Check{Name: name, Category: "readonly", Status: StatusFail, Detail: fmt.Sprintf("asked for %s %s, got a nuspec for %s %s", id, version, doc.Metadata.ID, doc.Metadata.Version)})
		return
	}
	r.Add(Check{Name: name, Category: "readonly", Status: StatusPass, Detail: fmt.Sprintf("id=%s version=%s", id, version)})
}

// checkUnlistedInFlat runs after a feed unlisted (not deleted) id/version:
// the flat container's version list "contains both listed and unlisted
// package versions", so it must still have it.
func checkUnlistedInFlat(c *client.Client, r *Report, id, version string) {
	const name = "unlisted version stays in flat container"
	flat, err := c.FlatContainerVersions(id)
	switch {
	case err != nil:
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s %s", id, version), Err: err.Error()})
	case !containsVersion(flat.Versions, version):
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s %s missing from %v after unlist", id, version, flat.Versions)})
	default:
		r.Add(Check{Name: name, Category: "push", Status: StatusPass})
	}
}

// runNormalizationChecks pushes meta with a non-normalized version (e.g.
// 1.00.0.0) and checks the feed treats it as its normalized form (1.0.0):
// the flat container lists 1.0.0, registration's catalogEntry.version is
// 1.0.0, the package downloads (with its .nuspec) via 1.0.0, and pushing an
// equivalent version string (1.0) is a duplicate. The push itself is
// recorded by the caller; data is the pushed .nupkg.
func runNormalizationChecks(c *client.Client, r *Report, meta pkgRef, data []byte) {
	const cat = "push"
	norm := normalizeVersion(meta.Version)

	var flat *client.FlatContainerVersions
	if !pollUntil(func() bool {
		var err error
		flat, err = c.FlatContainerVersions(meta.ID)
		return err == nil && len(flat.Versions) > 0
	}) {
		r.Add(Check{Name: "version normalized: flat container", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s never appeared in the flat container after %s", meta.ID, pollTimeout)})
		return
	}
	if len(flat.Versions) == 1 && flat.Versions[0] == strings.ToLower(norm) {
		r.Add(Check{Name: "version normalized: flat container", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("pushed %s, listed %s", meta.Version, flat.Versions[0])})
	} else {
		r.Add(Check{Name: "version normalized: flat container", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("pushed %s, want versions [%s], got %v", meta.Version, strings.ToLower(norm), flat.Versions)})
	}

	var entry *client.CatalogEntry
	if !pollUntil(func() bool {
		var err error
		entry, err = c.RegistrationVersion(meta.ID, norm)
		return err == nil && entry != nil && entry.Version != ""
	}) {
		r.Add(Check{Name: "version normalized: registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("no registration entry for %s %s after %s", meta.ID, norm, pollTimeout)})
	} else if entry.Version != norm {
		r.Add(Check{Name: "version normalized: registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("catalogEntry.version = %q, want %q (the spec: \"the full version string after normalization\")", entry.Version, norm)})
	} else {
		r.Add(Check{Name: "version normalized: registration", Category: cat, Status: StatusPass})
	}

	got, err := c.PullBytes(meta.ID, norm)
	switch {
	case err != nil:
		r.Add(Check{Name: "download via normalized version", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("%s %s", meta.ID, norm), Err: err.Error()})
	case sha512.Sum512(got) != sha512.Sum512(data):
		r.Add(Check{Name: "download via normalized version", Category: cat, Status: StatusFail, Detail: "downloaded bytes differ from what was pushed"})
	default:
		r.Add(Check{Name: "download via normalized version", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("%s %s", meta.ID, norm)})
	}

	checkPushedNuspec(c, r, meta, data)

	// "a repository that contains version 1.0 of a package should not also
	// host version 1.0.0 as a separate and different package". 409 is the
	// spec'd answer to pushing an existing version; a feed that replaces on
	// re-push (allowed: "Other package sources may support replacing an
	// existing package") may answer 2xx, as long as no second version shows
	// up.
	const dupName = "duplicate push of an equivalent version"
	dup := pkgRef{ID: meta.ID, Version: shortForm(norm)}
	dupData, err := buildNupkg(dup.ID, dup.Version, nil)
	if err == nil {
		err = c.PushBytes(fmt.Sprintf("%s.%s.nupkg", dup.ID, dup.Version), dupData)
	}
	var ce *client.ClientError
	switch {
	case err == nil:
		flat, ferr := c.FlatContainerVersions(meta.ID)
		if ferr == nil && len(flat.Versions) == 1 {
			r.Add(Check{Name: dupName, Category: cat, Status: StatusWarn, Detail: fmt.Sprintf("push of %s over %s accepted (feed replaces existing packages); still one version %v", dup.Version, norm, flat.Versions)})
		} else {
			detail := fmt.Sprintf("push of %s accepted next to %s", dup.Version, norm)
			if ferr == nil {
				detail += fmt.Sprintf("; flat container now lists %v", flat.Versions)
			}
			r.Add(Check{Name: dupName, Category: cat, Status: StatusFail, Detail: detail})
		}
	case asClientError(err, &ce) && ce.StatusCode == http.StatusConflict:
		r.Add(Check{Name: dupName, Category: cat, Status: StatusPass, Detail: fmt.Sprintf("push of %s after %s got 409", dup.Version, meta.Version)})
	default:
		r.Add(Check{Name: dupName, Category: cat, Status: StatusFail, Detail: fmt.Sprintf("push of %s after %s: want 409", dup.Version, meta.Version), Err: err.Error()})
	}
}

// shortForm drops trailing ".0" parts down to Major.Minor, the shortest
// spelling NuGet treats as the same version (1.0.0 -> 1.0).
func shortForm(v string) string {
	if strings.ContainsAny(v, "-+") {
		return v
	}
	for strings.Count(v, ".") > 1 && strings.HasSuffix(v, ".0") {
		v = strings.TrimSuffix(v, ".0")
	}
	return v
}

// checkPushedNuspec fetches the .nuspec of a package nugctl pushed. The
// spec says the response "will be the package manifest, which is the
// .nuspec contained in the corresponding .nupkg": byte-identical passes, a
// re-serialized manifest with the same id and version warns, anything else
// fails.
func checkPushedNuspec(c *client.Client, r *Report, p pkgRef, data []byte) {
	const name = "nuspec download matches pushed package"
	want, err := nuspecFromNupkg(data)
	if err != nil {
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Err: err.Error()})
		return
	}
	got, err := fetchNuspec(c, p.ID, p.Version)
	if err != nil {
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: fmt.Sprintf("%s %s", p.ID, normalizeVersion(p.Version)), Err: err.Error()})
		return
	}
	if bytes.Equal(got, want) {
		r.Add(Check{Name: name, Category: "push", Status: StatusPass, Detail: "byte-identical"})
		return
	}
	doc, err := parseNuspec(got)
	if err != nil || doc.Metadata.ID != p.ID || !sameVersion(doc.Metadata.Version, p.Version) {
		detail := "response is not the pushed package's nuspec"
		if err == nil {
			detail = fmt.Sprintf("nuspec says %s %s, pushed %s %s", doc.Metadata.ID, doc.Metadata.Version, p.ID, p.Version)
		}
		r.Add(Check{Name: name, Category: "push", Status: StatusFail, Detail: detail})
		return
	}
	r.Add(Check{Name: name, Category: "push", Status: StatusWarn, Detail: "same id and version, but not the bytes inside the .nupkg (re-serialized manifest)"})
}
