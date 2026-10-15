// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package version_test

import (
	"testing"

	"github.com/rauny-brandao/jig/version"
)

func TestParse(t *testing.T) {
	v, err := version.Parse("v1.2.3-rc.1+build.5")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	want := version.Version{Major: 1, Minor: 2, Patch: 3, Pre: "rc.1", Build: "build.5"}
	if v != want {
		t.Errorf("Parse() = %+v, want %+v", v, want)
	}
	if v.String() != "1.2.3-rc.1+build.5" {
		t.Errorf("String() = %q", v.String())
	}

	for _, bad := range []string{"", "1", "1.2", "1.2.3.4", "a.b.c", "1.+2.3", "1..3"} {
		if _, err := version.Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", bad)
		}
	}
}

func TestCompareFollowsSemverPrecedence(t *testing.T) {
	// From the semver specification, in ascending order.
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.1.0", "2.0.0", "10.0.0",
	}
	for i := range len(ordered) - 1 {
		a, b := mustParse(t, ordered[i]), mustParse(t, ordered[i+1])
		if a.Compare(b) != -1 || b.Compare(a) != 1 {
			t.Errorf("want %s < %s", ordered[i], ordered[i+1])
		}
	}
	if mustParse(t, "1.0.0+a").Compare(mustParse(t, "1.0.0+b")) != 0 {
		t.Error("build metadata must not affect ordering")
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		have, want string
		ok         bool
	}{
		{"2.1.0", "2.1.0", true},
		{"2.2.0", "2.1.0", true},
		{"v2.1.0", "2.1.0", true},
		{"2.0.9", "2.1.0", false},
		{"2.1.0-rc.1", "2.1.0", false},
		{"", "2.1.0", false},
		{"garbage", "2.1.0", false},
		{"2.1.0", "garbage", false},
	}
	for _, tt := range tests {
		if got := version.AtLeast(tt.have, tt.want); got != tt.ok {
			t.Errorf("AtLeast(%q, %q) = %v, want %v", tt.have, tt.want, got, tt.ok)
		}
	}
}

func TestFromOutput(t *testing.T) {
	tests := []struct {
		name, output, pattern, want string
	}{
		{"pattern", "gh version 2.102.0 (2026-01-01)\nhttps://example.com\n", "gh version {version}", "2.102.0"},
		{"literal punctuation after placeholder", "Some CLI 1.0.96.\nCommit: abc\n", "Some CLI {version}.", "1.0.96"},
		{"prefix in pattern", "v4.3.0", "v{version}", "4.3.0"},
		{"bare placeholder", "3.5.1\n", "{version}", "3.5.1"},
		{"regex characters are literal", "tool (x) 1.2.3", "tool (x) {version}", "1.2.3"},
		{"no pattern uses first semver", "tool build 7 version 1.2.3-beta+x", "", "1.2.3-beta+x"},
		{"no match", "unrelated", "tool {version}", ""},
		{"no semver", "nothing here", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := version.FromOutput(tt.output, tt.pattern); got != tt.want {
				t.Errorf("FromOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func mustParse(t *testing.T, s string) version.Version {
	t.Helper()
	v, err := version.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", s, err)
	}
	return v
}
