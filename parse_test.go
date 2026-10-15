// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

const _manifestYAML = `
# Settings for the embedding program are ignored by jig.
owner: platform-team
tools:
  gh:
    version: "2.1.0"
    description: GitHub CLI
    platforms:
      linux_amd64:
        url: "https://example.com/gh_{version}_linux_amd64.tar.gz"
        archive_format: tar.gz
        binary_name: gh
        binary_path: "gh_{version}_linux_amd64/bin/gh"
        sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    verify_command: ["gh", "version"]
    verify_pattern: "gh version {version}"
    cleanup_paths: [".gh/pkg"]
`

func TestParse(t *testing.T) {
	m, err := jig.Parse([]byte(_manifestYAML))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
	asset, err := m.Asset("gh", "linux_amd64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.ArchiveFormat != manifest.FormatTarGz || asset.BinaryPath != "gh_2.1.0_linux_amd64/bin/gh" {
		t.Errorf("asset = %+v", asset)
	}
	if got := m.Tools["gh"].CleanupPaths; len(got) != 1 || got[0] != ".gh/pkg" {
		t.Errorf("CleanupPaths = %v", got)
	}
}

func TestParseEmptyManifest(t *testing.T) {
	m, err := jig.Parse(nil)
	if err != nil {
		t.Fatalf("Parse(nil) error = %v", err)
	}
	if m.Tools == nil {
		t.Error("Tools is nil, want an empty map")
	}
}

func TestParseRejectsInvalidYAML(t *testing.T) {
	if _, err := jig.Parse([]byte("tools: [")); err == nil {
		t.Fatal("Parse() succeeded on invalid YAML")
	}
}

func TestParseExtras(t *testing.T) {
	m, err := jig.Parse([]byte(`
tools: {}
extras:
  - kind: uv-tool
    name: app
    source: "git+https://example.com/app.git"
    platforms: [darwin]
    reinstall: always
    hooks:
      post_install: ["--register"]
      pre_uninstall: ["--unregister"]
  - kind: node-runtime
    name: node
    channel: lts-latest
    default: true
    requires: [fnm]
`))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(m.Extras) != 2 {
		t.Fatalf("Extras = %+v", m.Extras)
	}
	app := m.Extras[0]
	if app.Kind != "uv-tool" || app.Param("source") != "git+https://example.com/app.git" || app.Reinstall != "always" {
		t.Errorf("app = %+v", app)
	}
	if len(app.Params) != 1 {
		t.Errorf("common fields leaked into Params: %v", app.Params)
	}
	if len(app.Hooks.PostInstall) != 1 || app.Hooks.PreUninstall[0] != "--unregister" {
		t.Errorf("hooks = %+v", app.Hooks)
	}
	node := m.Extras[1]
	if node.Param("default") != "true" || node.Param("channel") != "lts-latest" || node.Requires[0] != "fnm" {
		t.Errorf("node = %+v (unquoted YAML booleans must decode as strings)", node)
	}
}
