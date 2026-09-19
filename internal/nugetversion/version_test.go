package nugetversion

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in      string
		want    Version
		wantErr bool
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, false},
		{"1.2.3.4", Version{Major: 1, Minor: 2, Patch: 3, Revision: 4}, false},
		{"1.0", Version{Major: 1}, false},
		{"1", Version{Major: 1}, false},
		{"2.0.0-beta.1", Version{Major: 2, Prerelease: "beta.1"}, false},
		{"2.0.0-beta+build.5", Version{Major: 2, Prerelease: "beta"}, false},
		{"", Version{}, true},
		{"1.2.3.4.5", Version{}, true},
		{"1.x.3", Version{}, true},
		{"-1.0.0", Version{}, true},
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("Parse(%q) = %+v, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got.Major != tc.want.Major || got.Minor != tc.want.Minor || got.Patch != tc.want.Patch ||
			got.Revision != tc.want.Revision || got.Prerelease != tc.want.Prerelease {
			t.Errorf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.1", "1.0.0", 1},
		{"1.0", "1.0.0", 0}, // missing components are zero
		{"2.0.0", "1.9.9", 1},
		{"1.0.0-alpha", "1.0.0", -1}, // prerelease < release
		{"1.0.0", "1.0.0-alpha", 1},
		{"1.0.0-alpha", "1.0.0-beta", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.2", -1},
		{"1.0.0-alpha.1", "1.0.0-alpha.beta", -1}, // numeric < alphanumeric
		{"1.0.0-alpha", "1.0.0-alpha.1", -1},      // shorter < longer with shared prefix
		{"1.0.0-alpha", "1.0.0-alpha", 0},
		{"1.0.0-Alpha", "1.0.0-alpha", 0}, // case-insensitive identifiers
	}
	for _, tc := range cases {
		a, err := Parse(tc.a)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.a, err)
		}
		b, err := Parse(tc.b)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.b, err)
		}
		if got := Compare(a, b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
