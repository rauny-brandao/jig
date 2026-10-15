// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package manifest_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/rauny-brandao/jig/manifest"
)

func withExtras(extras ...manifest.Extra) *manifest.Manifest {
	return &manifest.Manifest{
		Tools:  map[string]manifest.Tool{"fnm": validTool(), "gh": validTool()},
		Extras: extras,
	}
}

func TestOrderPutsRequirementsFirst(t *testing.T) {
	m := withExtras(
		manifest.Extra{Kind: "npm-global", Name: "typescript", Requires: []string{"node"}},
		manifest.Extra{Kind: "gh-extension", Name: "stack", Requires: []string{"gh"}},
		manifest.Extra{Kind: "node-runtime", Name: "node", Requires: []string{"fnm"}},
	)

	order, err := m.Order()
	if err != nil {
		t.Fatalf("Order() error = %v", err)
	}
	want := []string{"fnm", "gh", "stack", "node", "typescript"}
	if !slices.Equal(order, want) {
		t.Errorf("Order() = %v, want %v", order, want)
	}
}

func TestValidateExtras(t *testing.T) {
	tests := []struct {
		name   string
		extras []manifest.Extra
		want   string
	}{
		{"missing name", []manifest.Extra{{Kind: "k"}}, "extras[0]: name is required"},
		{"missing kind", []manifest.Extra{{Name: "x"}}, "kind is required"},
		{"name used by a tool", []manifest.Extra{{Kind: "k", Name: "gh"}}, "already used"},
		{"duplicate extra", []manifest.Extra{{Kind: "k", Name: "x"}, {Kind: "k", Name: "x"}}, "already used"},
		{"bad reinstall", []manifest.Extra{{Kind: "k", Name: "x", Reinstall: "sometimes"}}, "reinstall must be"},
		{"self requirement", []manifest.Extra{{Kind: "k", Name: "x", Requires: []string{"x"}}}, "cannot require itself"},
		{"bad platform key", []manifest.Extra{{Kind: "k", Name: "x", Platforms: []string{"darwin_"}}}, "invalid platform key"},
		{"unknown requirement", []manifest.Extra{{Kind: "k", Name: "x", Requires: []string{"nope"}}}, `requires unknown item "nope"`},
		{"cycle", []manifest.Extra{
			{Kind: "k", Name: "a", Requires: []string{"b"}},
			{Kind: "k", Name: "b", Requires: []string{"a"}},
		}, "cycle among a, b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := withExtras(tt.extras...).Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateAcceptsExtras(t *testing.T) {
	m := withExtras(
		manifest.Extra{Kind: "node-runtime", Name: "node", Requires: []string{"fnm"}, Platforms: []string{"darwin", "linux_amd64"}},
		manifest.Extra{Kind: "uv-tool", Name: "app", Reinstall: manifest.ReinstallAlways},
	)
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestExtraAppliesTo(t *testing.T) {
	e := manifest.Extra{Platforms: []string{"darwin", "linux_arm64"}}
	for p, want := range map[manifest.Platform]bool{
		{OS: "darwin", Arch: "amd64"}:  true,
		{OS: "linux", Arch: "arm64"}:   true,
		{OS: "linux", Arch: "amd64"}:   false,
		{OS: "windows", Arch: "amd64"}: false,
	} {
		if got := e.AppliesTo(p); got != want {
			t.Errorf("AppliesTo(%s) = %v, want %v", p.Key(), got, want)
		}
	}
	if !(manifest.Extra{}).AppliesTo(manifest.Platform{OS: "plan9", Arch: "386"}) {
		t.Error("an extra without platforms must apply everywhere")
	}
}

func TestExtraLookup(t *testing.T) {
	m := withExtras(manifest.Extra{Kind: "k", Name: "x", Params: map[string]string{"repo": "a/b"}})
	e, ok := m.Extra("x")
	if !ok || e.Param("repo") != "a/b" || e.Param("missing") != "" {
		t.Errorf("Extra(x) = %+v, %v", e, ok)
	}
	if _, ok := m.Extra("y"); ok {
		t.Error("Extra(y) found a missing extra")
	}
}
