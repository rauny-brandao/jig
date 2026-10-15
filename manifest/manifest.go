// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package manifest describes the tools jig installs. It depends only on the
// standard library and performs no I/O, so programs that keep their core free
// of side effects can share these types directly.
package manifest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// VersionPlaceholder is replaced with a tool's version in asset URLs and paths.
const VersionPlaceholder = "{version}"

// Format says how a downloaded asset is unpacked.
type Format string

// Supported asset formats.
const (
	// FormatZip is a zip archive.
	FormatZip Format = "zip"
	// FormatTarGz is a gzip-compressed tar archive.
	FormatTarGz Format = "tar.gz"
	// FormatTarBz2 is a bzip2-compressed tar archive.
	FormatTarBz2 Format = "tar.bz2"
	// FormatGz is a single gzip-compressed binary.
	FormatGz Format = "gz"
	// FormatBinary is an uncompressed binary.
	FormatBinary Format = "binary"
	// FormatSDK is a zip or tar.gz archive whose SDKDir is kept intact
	// instead of extracting a single binary.
	FormatSDK Format = "sdk"
)

// Formats lists every supported format.
func Formats() []Format {
	return []Format{FormatZip, FormatTarGz, FormatTarBz2, FormatGz, FormatBinary, FormatSDK}
}

// Valid reports whether f is a supported format.
func (f Format) Valid() bool { return slices.Contains(Formats(), f) }

// Errors returned when looking up assets.
var (
	// ErrToolNotFound means the manifest does not declare the tool.
	ErrToolNotFound = errors.New("tool not found in manifest")
	// ErrPlatformNotSupported means the tool has no asset for the platform.
	ErrPlatformNotSupported = errors.New("platform not supported by tool")
)

// Manifest is the declarative list of tools and extras to install.
type Manifest struct {
	Tools map[string]Tool `yaml:"tools" json:"tools"`
	// Extras are installed through tools, after the items they require.
	Extras []Extra `yaml:"extras,omitempty" json:"extras,omitempty"`
}

// Tool is one pinned tool and the asset to download for each platform.
type Tool struct {
	// Version is the pinned version, substituted for {version} in assets.
	Version string `yaml:"version" json:"version"`
	// Description is a short human-readable summary.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Platforms maps a platform key such as "darwin_arm64" to its asset.
	Platforms map[string]Asset `yaml:"platforms" json:"platforms"`
	// VerifyCommand prints the installed version. Its first element names
	// the binary; jig runs the managed copy rather than whatever is on PATH.
	VerifyCommand []string `yaml:"verify_command" json:"verify_command"`
	// VerifyPattern locates the version in VerifyCommand's output, for
	// example "gh version {version}". When empty, the first semantic
	// version in the output is used.
	VerifyPattern string `yaml:"verify_pattern,omitempty" json:"verify_pattern,omitempty"`
	// CleanupPaths are home-relative paths removed after a successful
	// install, for stale files left behind by earlier install methods.
	CleanupPaths []string `yaml:"cleanup_paths,omitempty" json:"cleanup_paths,omitempty"`
}

// Asset describes how to download and unpack a tool on one platform.
type Asset struct {
	// URL is the download location; may contain {version}.
	URL string `yaml:"url" json:"url"`
	// ArchiveFormat says how the download is unpacked.
	ArchiveFormat Format `yaml:"archive_format" json:"archive_format"`
	// BinaryName is the file name the binary is installed as.
	BinaryName string `yaml:"binary_name" json:"binary_name"`
	// BinaryPath is the slash-separated path of the binary inside the
	// archive, when it differs from BinaryName; may contain {version}.
	BinaryPath string `yaml:"binary_path,omitempty" json:"binary_path,omitempty"`
	// ExtraBinaries are further slash-separated archive paths installed
	// next to the main binary under their base names; may contain {version}.
	ExtraBinaries []string `yaml:"extra_binaries,omitempty" json:"extra_binaries,omitempty"`
	// SDKDir is the archive directory kept intact for FormatSDK.
	SDKDir string `yaml:"sdk_dir,omitempty" json:"sdk_dir,omitempty"`
	// SHA256 is the hex-encoded SHA-256 of the downloaded file.
	SHA256 string `yaml:"sha256" json:"sha256"`
}

// Resolve returns a copy of a with {version} replaced in its URL and paths.
func (a Asset) Resolve(version string) Asset {
	a.URL = strings.ReplaceAll(a.URL, VersionPlaceholder, version)
	a.BinaryPath = strings.ReplaceAll(a.BinaryPath, VersionPlaceholder, version)
	a.SDKDir = strings.ReplaceAll(a.SDKDir, VersionPlaceholder, version)
	if a.ExtraBinaries != nil {
		extras := make([]string, len(a.ExtraBinaries))
		for i, extra := range a.ExtraBinaries {
			extras[i] = strings.ReplaceAll(extra, VersionPlaceholder, version)
		}
		a.ExtraBinaries = extras
	}
	return a
}

// Asset returns the tool's resolved asset for a platform key.
func (t Tool) Asset(platformKey string) (Asset, bool) {
	asset, ok := t.Platforms[platformKey]
	if !ok {
		return Asset{}, false
	}
	return asset.Resolve(t.Version), true
}

// Asset returns the resolved asset of the named tool for a platform key.
func (m *Manifest) Asset(tool, platformKey string) (Asset, error) {
	spec, ok := m.Tools[tool]
	if !ok {
		return Asset{}, fmt.Errorf("%q: %w", tool, ErrToolNotFound)
	}
	asset, ok := spec.Asset(platformKey)
	if !ok {
		return Asset{}, fmt.Errorf("%q on %s: %w", tool, platformKey, ErrPlatformNotSupported)
	}
	return asset, nil
}
