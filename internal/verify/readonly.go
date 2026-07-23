package verify

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
)

// requiredResourceTypes are the @type prefixes every conformant v3 feed
// must advertise for nugctl's core operations to work.
var requiredResourceTypes = []string{
	"SearchQueryService",
	"RegistrationsBaseUrl",
	"PackageBaseAddress",
	"PackagePublish/2.0.0",
}

func runReadonly(c *client.Client, opts Options, r *Report) {
	idx, err := c.ServiceIndex()
	if err != nil {
		r.Aborted = true
		r.AbortReason = fmt.Sprintf("could not fetch service index: %v", err)
		return
	}

	present := map[string]bool{}
	for _, t := range requiredResourceTypes {
		found := hasResourceType(idx, t)
		present[t] = found
		status := StatusPass
		detail := "advertised in service index"
		if !found {
			status = StatusFail
			detail = "not advertised in service index"
		}
		r.Add(Check{Name: "resource present: " + t, Category: "readonly", Status: status, Detail: detail})
	}

	if present["SearchQueryService"] {
		if _, err := c.Search("", 0, 1, false); err != nil {
			r.Add(Check{Name: "resource responds: SearchQueryService", Category: "readonly", Status: StatusFail, Detail: "query failed", Err: err.Error()})
		} else {
			r.Add(Check{Name: "resource responds: SearchQueryService", Category: "readonly", Status: StatusPass})
		}
	}

	r.Add(Check{
		Name: "resource responds: PackagePublish/2.0.0", Category: "readonly",
		Status: StatusSkip,
		Detail: "PUT-only endpoint; no safe read-only probe. Run with --push to exercise it.",
	})

	runPackageIntegrity(c, opts, r)
}

func hasResourceType(idx *client.ServiceIndex, typePrefix string) bool {
	for _, res := range idx.Resources {
		if strings.HasPrefix(res.Type, typePrefix) {
			return true
		}
	}
	return false
}

func runPackageIntegrity(c *client.Client, opts Options, r *Report) {
	const cat = "readonly"
	id := opts.Package
	if id == "" {
		res, err := c.Search("", 0, 1, false)
		if err != nil || len(res.Data) == 0 {
			skip := "no package available to test (feed appears empty; pass --package to specify one)"
			r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusSkip, Detail: skip})
			r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusSkip, Detail: skip})
			r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: skip})
			return
		}
		id = res.Data[0].ID
	}

	idx, err := c.Registration(id)
	if err != nil {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusFail, Detail: "fetching registration for " + id, Err: err.Error()})
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusSkip, Detail: "registration fetch failed"})
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: "registration fetch failed"})
		return
	}

	var regVersions []string
	malformed := idx.Count == 0 || len(idx.Items) == 0
	for _, page := range idx.Items {
		for _, leaf := range page.Items {
			if leaf.CatalogEntry.ID == "" || leaf.CatalogEntry.Version == "" {
				malformed = true
				continue
			}
			regVersions = append(regVersions, leaf.CatalogEntry.Version)
		}
	}
	if malformed || len(regVersions) == 0 {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("id=%s: missing pages/items or leaves without id/version", id)})
	} else {
		r.Add(Check{Name: "registration well-formed", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s, %d version(s)", id, len(regVersions))})
	}

	flat, err := c.FlatContainerVersions(id)
	if err != nil {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusFail, Detail: "fetching flat-container list", Err: err.Error()})
	} else if !sameVersionSet(regVersions, flat.Versions) {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("registration=%v flat-container=%v", regVersions, flat.Versions)})
	} else {
		r.Add(Check{Name: "flat-container matches registration", Category: cat, Status: StatusPass})
	}

	runHashCheck(c, id, idx, r)
}

// sameVersionSet compares two version lists case-insensitively, ignoring order.
func sameVersionSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	norm := func(vs []string) map[string]bool {
		m := make(map[string]bool, len(vs))
		for _, v := range vs {
			m[strings.ToLower(v)] = true
		}
		return m
	}
	am, bm := norm(a), norm(b)
	for v := range am {
		if !bm[v] {
			return false
		}
	}
	return true
}

func runHashCheck(c *client.Client, id string, idx *client.RegistrationIndex, r *Report) {
	const cat = "readonly"
	var target *client.CatalogEntry
	var version string
	for _, page := range idx.Items {
		for i := range page.Items {
			e := &page.Items[i].CatalogEntry
			if e.PackageHash != "" {
				target = e
				version = e.Version
				break
			}
		}
	}
	if target == nil {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusSkip, Detail: "no version in the registration advertises packageHash"})
		return
	}

	data, err := c.PullBytes(id, version)
	if err != nil {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("downloading %s %s", id, version), Err: err.Error()})
		return
	}
	sum := sha512.Sum512(data)
	got := base64.StdEncoding.EncodeToString(sum[:])
	if got != target.PackageHash {
		r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusFail, Detail: fmt.Sprintf("id=%s version=%s registration=%s computed=%s", id, version, target.PackageHash, got)})
		return
	}
	r.Add(Check{Name: "package hash matches registration", Category: cat, Status: StatusPass, Detail: fmt.Sprintf("id=%s version=%s", id, version)})
}
