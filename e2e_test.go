// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/host"
	"github.com/rauny-brandao/jig/kinds"
	"github.com/rauny-brandao/jig/manifest"
)

// TestEndToEnd installs the example manifest for real, then removes it: it
// downloads and verifies release assets, unpacks them, runs the installed
// binaries, and installs and removes extras through gh, uv, fnm and npm. The
// JDK is left out because it is a large download. Every tool's data
// directory is redirected into a temporary directory, so nothing outside it
// changes. It needs network access (and, for the gh extension, gh being
// logged in or GH_TOKEN), so it only runs when JIG_E2E=1.
func TestEndToEnd(t *testing.T) {
	if os.Getenv("JIG_E2E") != "1" {
		t.Skip("set JIG_E2E=1 to download, install and remove the example tools")
	}
	root := t.TempDir()
	isolate(t, root)

	data, err := os.ReadFile(filepath.Join("examples", "tools.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := jig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	dirs := jig.Dirs{Base: root, Bin: filepath.Join(root, "bin"), Cache: filepath.Join(root, "cache"), Home: root}
	platform := manifest.Current()
	j, err := jig.New(host.Deps(), dirs, platform, jig.WithHandlers(kinds.Defaults()...))
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Validate(m); err != nil {
		t.Fatalf("example manifest is invalid: %v", err)
	}
	// The JDK is a large download, so it only runs with JIG_E2E_JDK=1.
	skipped := "temurin:25"
	if os.Getenv("JIG_E2E_JDK") == "1" {
		skipped = ""
	}
	var names []string
	order, err := m.Order()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range order {
		if name != skipped {
			names = append(names, name)
		}
	}

	before := status(t, j, m)
	results, err := j.Apply(t.Context(), j.Plan(before, jig.PlanOptions{Only: names}), func(e jig.Event) {
		t.Logf("%s %s %s", e.Type, e.Action.Op, e.Action.Name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := results.Err(); err != nil {
		t.Fatalf("install failed: %v", err)
	}

	installed := status(t, j, m)
	for _, s := range installed {
		if s.Name == skipped || s.State == jig.StateUnsupported {
			continue
		}
		if s.State != jig.StateOK {
			t.Errorf("%s after install: %s (have %q, want %q, err %v)", s.Name, s.State, s.Have, s.Want, s.Err)
		}
		t.Logf("%-10s %-12s %s", s.Name, s.Kind, s.Have)
	}
	if !j.Plan(installed, jig.PlanOptions{Only: names}).Empty() {
		t.Error("a second install plan is not empty")
	}
	if _, err := os.Stat(filepath.Join(dirs.Bin, executable(platform, "cowsay"))); err != nil {
		t.Errorf("uv tool executable not in the bin directory: %v", err)
	}

	// Extras are removed and checked while the tools that detect them are
	// still installed, so an extra that comes back is noticed.
	remove(t, j, j.PlanRemove(installed, jig.RemoveOptions{ExtrasOnly: true}))
	remaining := status(t, j, m)
	for _, s := range remaining {
		if s.Kind != jig.KindTool && s.State.Installed() {
			t.Errorf("%s still installed after removal", s.Name)
		}
	}

	remove(t, j, j.PlanRemove(remaining, jig.RemoveOptions{}))
	for _, s := range status(t, j, m) {
		if s.State.Installed() {
			t.Errorf("%s still installed after removal", s.Name)
		}
	}
}

func remove(t *testing.T, j *jig.Jig, plan jig.Plan) {
	t.Helper()
	results, err := j.Apply(t.Context(), plan, func(e jig.Event) {
		t.Logf("%s %s %s", e.Type, e.Action.Op, e.Action.Name)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := results.Err(); err != nil {
		t.Fatalf("removal failed: %v", err)
	}
}

func status(t *testing.T, j *jig.Jig, m *manifest.Manifest) []jig.Status {
	t.Helper()
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	return statuses
}

// isolate points every tool's data directory into root.
func isolate(t *testing.T, root string) {
	t.Helper()
	for key, dir := range map[string]string{
		"XDG_DATA_HOME":          "data",     // gh extensions
		"UV_TOOL_DIR":            "uv-tools", // uv tool environments
		"UV_CACHE_DIR":           "uv-cache",
		"UV_PYTHON_INSTALL_DIR":  "uv-python",
		"FNM_DIR":                "fnm", // Node.js runtimes and their global packages
		"COURSIER_CACHE":         "coursier/v1",
		"COURSIER_ARCHIVE_CACHE": "coursier/arc",
		"COURSIER_JVM_CACHE":     "coursier/jvm",
	} {
		t.Setenv(key, filepath.Join(root, dir))
	}
}

func executable(p manifest.Platform, name string) string {
	if p.OS == "windows" {
		return name + ".exe"
	}
	return name
}
