// Package nugetversion parses NuGet package versions and dependency version
// ranges (https://learn.microsoft.com/nuget/concepts/package-versioning) and
// answers whether a given version satisfies a given range. It has no
// dependency on internal/client so it can be unit tested in isolation.
package nugetversion

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a parsed NuGet version: up to four numeric components plus an
// optional prerelease label. Build metadata after "+" is parsed but ignored
// in comparisons, matching NuGet's SemVer2 handling.
type Version struct {
	Major, Minor, Patch, Revision int
	Prerelease                    string
	original                      string
}

// Parse parses a NuGet version string such as "1.2.3", "1.2.3.4", or
// "2.0.0-beta.1+build5".
func Parse(s string) (Version, error) {
	orig := s
	s = strings.TrimSpace(s)
	if s == "" {
		return Version{}, fmt.Errorf("empty version string")
	}

	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}

	var prerelease string
	if i := strings.IndexByte(s, '-'); i >= 0 {
		prerelease = s[i+1:]
		s = s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return Version{}, fmt.Errorf("invalid version %q", orig)
	}
	var nums [4]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q: component %q is not a non-negative integer", orig, p)
		}
		nums[i] = n
	}

	return Version{
		Major:      nums[0],
		Minor:      nums[1],
		Patch:      nums[2],
		Revision:   nums[3],
		Prerelease: prerelease,
		original:   orig,
	}, nil
}

// String returns the original text Parse was given.
func (v Version) String() string {
	if v.original != "" {
		return v.original
	}
	s := fmt.Sprintf("%d.%d.%d.%d", v.Major, v.Minor, v.Patch, v.Revision)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	return s
}

// Compare returns -1, 0, or 1 as a is less than, equal to, or greater than
// b, per NuGet/SemVer2 precedence: numeric components compare in order, and
// a version with no prerelease label always outranks one with a prerelease
// label at the same Major.Minor.Patch.Revision.
func Compare(a, b Version) int {
	if d := a.Major - b.Major; d != 0 {
		return sign(d)
	}
	if d := a.Minor - b.Minor; d != 0 {
		return sign(d)
	}
	if d := a.Patch - b.Patch; d != 0 {
		return sign(d)
	}
	if d := a.Revision - b.Revision; d != 0 {
		return sign(d)
	}
	return comparePrerelease(a.Prerelease, b.Prerelease)
}

func sign(d int) int {
	switch {
	case d < 0:
		return -1
	case d > 0:
		return 1
	default:
		return 0
	}
}

// comparePrerelease implements SemVer2 prerelease precedence: dot-separated
// identifiers compare left to right, numeric identifiers compare
// numerically and always sort below alphanumeric ones, and a shorter set of
// identifiers sorts below a longer one that shares the same prefix.
func comparePrerelease(a, b string) int {
	if a == "" && b == "" {
		return 0
	}
	if a == "" {
		return 1
	}
	if b == "" {
		return -1
	}

	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; ; i++ {
		if i >= len(aParts) && i >= len(bParts) {
			return 0
		}
		if i >= len(aParts) {
			return -1
		}
		if i >= len(bParts) {
			return 1
		}
		ai, bi := aParts[i], bParts[i]
		an, aerr := strconv.Atoi(ai)
		bn, berr := strconv.Atoi(bi)
		switch {
		case aerr == nil && berr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aerr == nil && berr != nil:
			return -1
		case aerr != nil && berr == nil:
			return 1
		default:
			al, bl := strings.ToLower(ai), strings.ToLower(bi)
			if al != bl {
				if al < bl {
					return -1
				}
				return 1
			}
		}
	}
}
