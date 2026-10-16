// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package manifest_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/rauny-brandao/jig/manifest"
)

const _sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func validTool() manifest.Tool {
	return manifest.Tool{
		Version: "2.1.0",
		Platforms: map[string]manifest.Asset{
			"linux_amd64": {
				URL:           "https://example.com/tool/{version}/tool_{version}_linux_amd64.tar.gz",
				ArchiveFormat: manifest.FormatTarGz,
				BinaryName:    "tool",
				BinaryPath:    "tool_{version}/bin/tool",
				ExtraBinaries: []string{"tool_{version}/bin/toolx"},
				SHA256:        _sha,
			},
		},
		VerifyCommand: []string{"tool", "--version"},
		VerifyPattern: "tool {version}",
	}
}

func TestAssetResolvesVersionPlaceholders(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{"tool": validTool()}}

	asset, err := m.Asset("tool", "linux_amd64")
	if err != nil {
		t.Fatalf("Asset() error = %v", err)
	}
	if want := "https://example.com/tool/2.1.0/tool_2.1.0_linux_amd64.tar.gz"; asset.URL != want {
		t.Errorf("URL = %q, want %q", asset.URL, want)
	}
	if want := "tool_2.1.0/bin/tool"; asset.BinaryPath != want {
		t.Errorf("BinaryPath = %q, want %q", asset.BinaryPath, want)
	}
	if want := []string{"tool_2.1.0/bin/toolx"}; !slices.Equal(asset.ExtraBinaries, want) {
		t.Errorf("ExtraBinaries = %q, want %q", asset.ExtraBinaries, want)
	}
}

func TestResolveDoesNotMutateTheManifest(t *testing.T) {
	tool := validTool()
	_, _ = tool.Asset("linux_amd64")

	if got := tool.Platforms["linux_amd64"].ExtraBinaries[0]; got != "tool_{version}/bin/toolx" {
		t.Errorf("manifest extra binary changed to %q", got)
	}
}

func TestAssetLookupErrors(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{"tool": validTool()}}

	if _, err := m.Asset("missing", "linux_amd64"); !errors.Is(err, manifest.ErrToolNotFound) {
		t.Errorf("unknown tool: error = %v, want ErrToolNotFound", err)
	}
	if _, err := m.Asset("tool", "plan9_386"); !errors.Is(err, manifest.ErrPlatformNotSupported) {
		t.Errorf("unknown platform: error = %v, want ErrPlatformNotSupported", err)
	}
}

func TestParseKey(t *testing.T) {
	p, err := manifest.ParseKey("darwin_arm64")
	if err != nil || p != (manifest.Platform{OS: "darwin", Arch: "arm64"}) {
		t.Fatalf("ParseKey(darwin_arm64) = %+v, %v", p, err)
	}
	if p.Key() != "darwin_arm64" {
		t.Errorf("Key() = %q", p.Key())
	}
	for _, bad := range []string{"", "darwin", "_arm64", "darwin_", "linux_arm_v7"} {
		if _, err := manifest.ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) succeeded, want error", bad)
		}
	}
}

func TestValidateAcceptsAValidManifest(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{"tool": validTool()}}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestValidateReportsProblems(t *testing.T) {
	tests := []struct {
		name   string
		tool   string
		modify func(*manifest.Tool, *manifest.Asset)
		want   string
	}{
		{"tool name with separator", "a/b", func(*manifest.Tool, *manifest.Asset) {}, "plain file name"},
		{"missing version", "tool", func(t *manifest.Tool, _ *manifest.Asset) { t.Version = "" }, "version is required"},
		{"missing verify command", "tool", func(t *manifest.Tool, _ *manifest.Asset) { t.VerifyCommand = nil }, "verify_command is required"},
		{"pattern without placeholder", "tool", func(t *manifest.Tool, _ *manifest.Asset) { t.VerifyPattern = "tool" }, "verify_pattern must contain"},
		{"cleanup path escapes home", "tool", func(t *manifest.Tool, _ *manifest.Asset) { t.CleanupPaths = []string{"../etc"} }, "cleanup_paths"},
		{"absolute cleanup path", "tool", func(t *manifest.Tool, _ *manifest.Asset) { t.CleanupPaths = []string{"/etc"} }, "cleanup_paths"},
		{"missing url", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.URL = "" }, "url is required"},
		{"unknown format", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.ArchiveFormat = "rar" }, "archive_format"},
		{"binary name with path", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.BinaryName = "bin/tool" }, "binary_name"},
		{"missing sha256", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.SHA256 = "" }, "sha256"},
		{"uppercase sha256", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.SHA256 = strings.ToUpper(_sha) }, "sha256"},
		{"binary path escapes archive", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.BinaryPath = "../tool" }, "binary_path"},
		{"windows path", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.BinaryPath = `bin\tool` }, "binary_path"},
		{"extra binary escapes archive", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.ExtraBinaries = []string{"a/../../x"} }, "extra_binaries"},
		{"sdk without sdk_dir", "tool", func(_ *manifest.Tool, a *manifest.Asset) {
			a.ArchiveFormat, a.BinaryPath, a.ExtraBinaries = manifest.FormatSDK, "", nil
		}, "sdk_dir"},
		{"sdk_dir without sdk format", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.SDKDir = "sdk" }, "sdk_dir only applies"},
		{"binary format with binary path", "tool", func(_ *manifest.Tool, a *manifest.Asset) { a.ArchiveFormat = manifest.FormatBinary }, "do not apply"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool := validTool()
			asset := tool.Platforms["linux_amd64"]
			tt.modify(&tool, &asset)
			tool.Platforms["linux_amd64"] = asset
			m := &manifest.Manifest{Tools: map[string]manifest.Tool{tt.tool: tool}}

			err := m.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	m := &manifest.Manifest{Tools: map[string]manifest.Tool{
		"a": {},
		"b": {Version: "1.0.0", VerifyCommand: []string{"b"}, Platforms: map[string]manifest.Asset{"bad": {}}},
	}}
	err := m.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want errors")
	}
	for _, want := range []string{`tool "a": version is required`, `tool "b": invalid platform key "bad"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() missing %q in:\n%v", want, err)
		}
	}
}
