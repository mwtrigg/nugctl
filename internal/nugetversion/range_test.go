package nugetversion

import "testing"

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func TestParseRange_Bounds(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantMin      string
		wantMinIncl  bool
		wantMax      string
		wantMaxIncl  bool
		wantMinIsNil bool
		wantMaxIsNil bool
	}{
		{name: "plain minimum inclusive", in: "1.0.0", wantMin: "1.0.0", wantMinIncl: true, wantMaxIsNil: true},
		{name: "exact", in: "[1.0.0]", wantMin: "1.0.0", wantMinIncl: true, wantMax: "1.0.0", wantMaxIncl: true},
		{name: "minimum exclusive open", in: "(1.0.0,)", wantMin: "1.0.0", wantMinIncl: false, wantMaxIsNil: true},
		{name: "minimum inclusive open", in: "[1.0.0,)", wantMin: "1.0.0", wantMinIncl: true, wantMaxIsNil: true},
		{name: "maximum inclusive open", in: "(,1.0.0]", wantMinIsNil: true, wantMax: "1.0.0", wantMaxIncl: true},
		{name: "maximum exclusive open", in: "(,1.0.0)", wantMinIsNil: true, wantMax: "1.0.0", wantMaxIncl: false},
		{name: "inclusive range", in: "[1.0.0,2.0.0]", wantMin: "1.0.0", wantMinIncl: true, wantMax: "2.0.0", wantMaxIncl: true},
		{name: "exclusive range", in: "(1.0.0,2.0.0)", wantMin: "1.0.0", wantMinIncl: false, wantMax: "2.0.0", wantMaxIncl: false},
		{name: "mixed range", in: "[1.0.0,2.0.0)", wantMin: "1.0.0", wantMinIncl: true, wantMax: "2.0.0", wantMaxIncl: false},
		{name: "empty means any version", in: "", wantMinIsNil: true, wantMaxIsNil: true},
		{name: "both bounds empty, exclusive form, means any version", in: "(,)", wantMinIsNil: true, wantMaxIsNil: true},
		{name: "both bounds empty, inclusive form, means any version", in: "[,]", wantMinIsNil: true, wantMaxIsNil: true},
		{name: "both bounds empty with whitespace means any version", in: "( , )", wantMinIsNil: true, wantMaxIsNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseRange(tc.in)
			if err != nil {
				t.Fatalf("ParseRange(%q): %v", tc.in, err)
			}
			if tc.wantMinIsNil != (r.MinVersion == nil) {
				t.Errorf("MinVersion nil-ness = %v, want %v", r.MinVersion == nil, tc.wantMinIsNil)
			} else if r.MinVersion != nil {
				want := mustParse(t, tc.wantMin)
				if Compare(*r.MinVersion, want) != 0 || r.MinInclusive != tc.wantMinIncl {
					t.Errorf("min = %v incl=%v, want %v incl=%v", r.MinVersion, r.MinInclusive, tc.wantMin, tc.wantMinIncl)
				}
			}
			if tc.wantMaxIsNil != (r.MaxVersion == nil) {
				t.Errorf("MaxVersion nil-ness = %v, want %v", r.MaxVersion == nil, tc.wantMaxIsNil)
			} else if r.MaxVersion != nil {
				want := mustParse(t, tc.wantMax)
				if Compare(*r.MaxVersion, want) != 0 || r.MaxInclusive != tc.wantMaxIncl {
					t.Errorf("max = %v incl=%v, want %v incl=%v", r.MaxVersion, r.MaxInclusive, tc.wantMax, tc.wantMaxIncl)
				}
			}
		})
	}
}

func TestParseRange_Invalid(t *testing.T) {
	cases := []string{
		"[1.0.0",
		"1.0.0]",
		"(1.0.0)", // exact match must use [ ]
		"[abc,2.0.0]",
	}
	for _, in := range cases {
		if _, err := ParseRange(in); err == nil {
			t.Errorf("ParseRange(%q) = nil error, want error", in)
		}
	}
}

func TestRange_Satisfies(t *testing.T) {
	cases := []struct {
		name string
		rng  string
		v    string
		want bool
	}{
		{"minimum inclusive, equal", "1.0.0", "1.0.0", true},
		{"minimum inclusive, below", "1.0.0", "0.9.0", false},
		{"minimum inclusive, above", "1.0.0", "1.0.1", true},
		{"exact match", "[1.0.0]", "1.0.0", true},
		{"exact mismatch", "[1.0.0]", "1.0.1", false},
		{"minimum exclusive, equal excluded", "(1.0.0,)", "1.0.0", false},
		{"minimum exclusive, above included", "(1.0.0,)", "1.0.1", true},
		{"inclusive range, lower bound", "[1.0.0,2.0.0]", "1.0.0", true},
		{"inclusive range, upper bound", "[1.0.0,2.0.0]", "2.0.0", true},
		{"inclusive range, below", "[1.0.0,2.0.0]", "0.9.9", false},
		{"inclusive range, above", "[1.0.0,2.0.0]", "2.0.1", false},
		{"exclusive range, bounds excluded", "(1.0.0,2.0.0)", "1.0.0", false},
		{"exclusive range, bounds excluded upper", "(1.0.0,2.0.0)", "2.0.0", false},
		{"exclusive range, inside", "(1.0.0,2.0.0)", "1.5.0", true},
		{"mixed range, lower included", "[1.0.0,2.0.0)", "1.0.0", true},
		{"mixed range, upper excluded", "[1.0.0,2.0.0)", "2.0.0", false},
		{"empty range accepts anything", "", "0.0.1", true},
		{"both-bounds-empty paren form accepts anything", "(,)", "0.0.1", true},
		{"both-bounds-empty bracket form accepts anything", "[,]", "0.0.1", true},
		{"prerelease below release minimum", "1.0.0", "1.0.0-beta", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := ParseRange(tc.rng)
			if err != nil {
				t.Fatalf("ParseRange(%q): %v", tc.rng, err)
			}
			v := mustParse(t, tc.v)
			if got := r.Satisfies(v); got != tc.want {
				t.Errorf("Range(%q).Satisfies(%q) = %v, want %v", tc.rng, tc.v, got, tc.want)
			}
		})
	}
}
