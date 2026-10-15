// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package version parses and compares semantic versions and extracts them
// from the output of a tool's version command. It depends only on the
// standard library and performs no I/O.
package version

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a semantic version: MAJOR.MINOR.PATCH with optional pre-release
// and build metadata.
type Version struct {
	Major, Minor, Patch int
	// Pre is the dot-separated pre-release, without the leading "-".
	Pre string
	// Build is the build metadata, without the leading "+". It never
	// affects ordering.
	Build string
}

// Parse parses s as a semantic version. A leading "v" is accepted.
func Parse(s string) (Version, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(s), "v")
	var v Version
	rest, v.Build, _ = strings.Cut(rest, "+")
	rest, v.Pre, _ = strings.Cut(rest, "-")

	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("parse version %q: want MAJOR.MINOR.PATCH", s)
	}
	for i, field := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil || strings.HasPrefix(parts[i], "+") {
			return Version{}, fmt.Errorf("parse version %q: invalid number %q", s, parts[i])
		}
		*field = n
	}
	return v, nil
}

// String formats the version in canonical form, without a leading "v".
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	if v.Build != "" {
		s += "+" + v.Build
	}
	return s
}

// Compare returns -1, 0 or +1 depending on whether v sorts before, equal to,
// or after other, following semantic versioning precedence.
func (v Version) Compare(other Version) int {
	if c := cmp.Compare(v.Major, other.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Minor, other.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Patch, other.Patch); c != 0 {
		return c
	}
	return comparePre(v.Pre, other.Pre)
}

// comparePre orders pre-releases: a release sorts after any pre-release,
// numeric identifiers compare numerically and sort before alphanumeric ones,
// and a shorter list of otherwise equal identifiers sorts first.
func comparePre(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range min(len(as), len(bs)) {
		if c := compareIdentifier(as[i], bs[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

func compareIdentifier(a, b string) int {
	an, aErr := strconv.Atoi(a)
	bn, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(an, bn)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

var _semverInText = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)

// FromOutput extracts a version from a version command's output. With a
// pattern such as "gh version {version}", the text matched by {version} is
// returned; without one, the first semantic version in the output is. It
// returns "" when nothing matches.
func FromOutput(output, pattern string) string {
	if pattern == "" {
		return _semverInText.FindString(output)
	}
	quoted := regexp.QuoteMeta(pattern)
	placeholder := regexp.QuoteMeta("{version}")
	expr := strings.Replace(quoted, placeholder, `(\S+)`, 1)
	expr = strings.ReplaceAll(expr, placeholder, `\S+`)
	re, err := regexp.Compile(expr)
	if err != nil {
		return ""
	}
	if match := re.FindStringSubmatch(output); len(match) > 1 {
		return match[1]
	}
	return ""
}
