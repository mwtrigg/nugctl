package verify

import (
	"testing"

	"github.com/mwtrigg/nugctl/internal/client"
)

func TestCheckClientTypeStrings(t *testing.T) {
	idx := &client.ServiceIndex{Resources: []client.Resource{
		{ID: "http://x/search", Type: "SearchQueryService/3.0.0-rc"}, // documented alias, but no client looks it up
		{ID: "http://x/reg", Type: "RegistrationsBaseUrl/3.6.0"},
		{ID: "http://x/flat", Type: "PackageBaseAddress/3.0.0"},
		{ID: "http://x/pub", Type: "PackagePublish/2.0.0"},
	}}
	r := &Report{}
	checkClientTypeStrings(idx, r)

	want := map[string]Status{
		"client type string: SearchQueryService":   StatusWarn,
		"client type string: RegistrationsBaseUrl": StatusPass,
		"client type string: PackageBaseAddress":   StatusPass,
		"client type string: PackagePublish":       StatusPass,
	}
	for name, st := range want {
		if got := checkStatus(r, name); got != st {
			t.Errorf("%s = %q, want %s", name, got, st)
		}
	}
	if got := checkStatus(r, "client type string: SearchAutocompleteService"); got != "" {
		t.Errorf("autocomplete not advertised, so no check expected; got %s", got)
	}
}

func TestCheckClientTypeStrings_CaseSensitive(t *testing.T) {
	idx := &client.ServiceIndex{Resources: []client.Resource{
		{ID: "http://x/flat", Type: "PackageBaseAddress/3.0.0"},
		{ID: "http://x/pub", Type: "PackagePublish/2.0.0"},
		{ID: "http://x/ac", Type: "SearchAutocompleteService/3.0.0-Beta"},
	}}
	r := &Report{}
	checkClientTypeStrings(idx, r)
	if got := checkStatus(r, "client type string: SearchAutocompleteService"); got != StatusWarn {
		t.Errorf("case-mismatched type = %s, want warn (NuGet.Protocol compares ordinally)", got)
	}
}
