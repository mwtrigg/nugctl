package nugetversion

import (
	"fmt"
	"strings"
)

// Range is a parsed NuGet dependency version range
// (https://learn.microsoft.com/nuget/concepts/package-versioning#version-ranges).
// A nil MinVersion/MaxVersion means that bound is unset.
type Range struct {
	MinVersion   *Version
	MinInclusive bool
	MaxVersion   *Version
	MaxInclusive bool
}

// ParseRange parses a NuGet version range, e.g. "1.0.0" (minimum inclusive),
// "[1.0.0]" (exact), "(1.0.0,)" (minimum exclusive), "[1.0.0,2.0.0)"
// (mixed-inclusive range). An empty string means "any version".
func ParseRange(s string) (*Range, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return &Range{}, nil
	}

	if s[0] != '[' && s[0] != '(' {
		v, err := Parse(s)
		if err != nil {
			return nil, err
		}
		return &Range{MinVersion: &v, MinInclusive: true}, nil
	}

	if len(s) < 2 {
		return nil, fmt.Errorf("invalid version range %q", s)
	}
	open := s[0]
	closeCh := s[len(s)-1]
	if closeCh != ']' && closeCh != ')' {
		return nil, fmt.Errorf("invalid version range %q", s)
	}
	minInclusive := open == '['
	maxInclusive := closeCh == ']'
	inner := s[1 : len(s)-1]

	if !strings.Contains(inner, ",") {
		if !minInclusive || !maxInclusive {
			return nil, fmt.Errorf("invalid version range %q: exact-match range must be [version]", s)
		}
		v, err := Parse(inner)
		if err != nil {
			return nil, fmt.Errorf("invalid version range %q: %w", s, err)
		}
		return &Range{MinVersion: &v, MinInclusive: true, MaxVersion: &v, MaxInclusive: true}, nil
	}

	parts := strings.SplitN(inner, ",", 2)
	minStr, maxStr := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	r := &Range{}
	if minStr != "" {
		v, err := Parse(minStr)
		if err != nil {
			return nil, fmt.Errorf("invalid version range %q: %w", s, err)
		}
		r.MinVersion = &v
		r.MinInclusive = minInclusive
	}
	if maxStr != "" {
		v, err := Parse(maxStr)
		if err != nil {
			return nil, fmt.Errorf("invalid version range %q: %w", s, err)
		}
		r.MaxVersion = &v
		r.MaxInclusive = maxInclusive
	}
	if r.MinVersion == nil && r.MaxVersion == nil {
		return nil, fmt.Errorf("invalid version range %q: no bounds given", s)
	}
	return r, nil
}

// Satisfies reports whether v falls within r.
func (r *Range) Satisfies(v Version) bool {
	if r.MinVersion != nil {
		c := Compare(v, *r.MinVersion)
		if r.MinInclusive {
			if c < 0 {
				return false
			}
		} else if c <= 0 {
			return false
		}
	}
	if r.MaxVersion != nil {
		c := Compare(v, *r.MaxVersion)
		if r.MaxInclusive {
			if c > 0 {
				return false
			}
		} else if c >= 0 {
			return false
		}
	}
	return true
}
