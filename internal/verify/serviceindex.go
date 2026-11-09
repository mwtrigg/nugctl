package verify

import (
	"fmt"
	"strings"

	"github.com/mwtrigg/nugctl/internal/client"
)

// clientTypeStrings are the exact @type strings NuGet's own client
// (NuGet.Protocol) looks up for each resource. The list is copied from
// NuGet.Client src/NuGet.Core/NuGet.Protocol/ServiceTypes.cs at commit
// af6ebf6f4b33cef424aa687d0090d492f71e84dc. NuGet.Protocol matches @type
// with an ordinal (exact, case-sensitive) comparer
// (Resources/ServiceIndexResourceV3.cs), so a feed whose resource only
// starts with the right prefix, e.g. "SearchQueryService/3.0.0-rc", is
// invisible to dotnet, nuget.exe and Visual Studio even though it is a
// documented alias
// (https://learn.microsoft.com/nuget/api/search-query-service-resource#versioning).
// "X/Versioned" entries carry a clientVersion list; their @type string is
// still matched exactly.
var clientTypeStrings = []struct {
	prefix string
	exact  []string
}{
	{"SearchQueryService", []string{"SearchQueryService/Versioned", "SearchQueryService/3.4.0", "SearchQueryService/3.0.0-beta"}},
	{"RegistrationsBaseUrl", []string{"RegistrationsBaseUrl/Versioned", "RegistrationsBaseUrl/3.6.0", "RegistrationsBaseUrl/3.4.0", "RegistrationsBaseUrl/3.0.0-rc", "RegistrationsBaseUrl/3.0.0-beta", "RegistrationsBaseUrl"}},
	{"PackageBaseAddress", []string{"PackageBaseAddress/Versioned", "PackageBaseAddress/3.0.0"}},
	{"PackagePublish", []string{"PackagePublish/Versioned", "PackagePublish/2.0.0"}},
	{"SearchAutocompleteService", []string{"SearchAutocompleteService/Versioned", "SearchAutocompleteService/3.0.0-beta"}},
}

// checkClientTypeStrings warns, per resource the feed advertises by
// prefix, when none of its @type values is a string NuGet's client
// actually looks up. A warning, not a failure: the spec documents other
// aliases, and other clients may accept them.
func checkClientTypeStrings(idx *client.ServiceIndex, r *Report) {
	for _, ct := range clientTypeStrings {
		var advertised []string
		for _, res := range idx.Resources {
			if strings.HasPrefix(res.Type, ct.prefix) {
				advertised = append(advertised, res.Type)
			}
		}
		if len(advertised) == 0 {
			continue // "resource present" already reports a missing required resource
		}
		name := "client type string: " + ct.prefix
		if hit := firstExact(advertised, ct.exact); hit != "" {
			r.Add(Check{Name: name, Category: "readonly", Status: StatusPass, Detail: hit})
			continue
		}
		r.Add(Check{Name: name, Category: "readonly", Status: StatusWarn,
			Detail: fmt.Sprintf("advertised %v, but NuGet's client looks up only %v (exact match); dotnet/nuget.exe will not find this resource", advertised, ct.exact)})
	}
}

func firstExact(have, want []string) string {
	for _, w := range want {
		for _, h := range have {
			if h == w {
				return h
			}
		}
	}
	return ""
}
