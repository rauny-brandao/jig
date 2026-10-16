// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package manifest

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rauny-brandao/jig/internal/check"
)

// Validate reports every problem in the manifest, joined into one error.
// It returns nil when the manifest is safe to install from.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("manifest is nil")
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(m.Tools)) {
		if err := m.Tools[name].validate(name); err != nil {
			errs = append(errs, err)
		}
	}
	errs = append(errs, m.validateExtras()...)
	return errors.Join(errs...)
}

func (t Tool) validate(name string) error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf("tool %q: "+format, append([]any{name}, args...)...))
	}

	if !check.IsPlainName(name) {
		fail("name must be a plain file name")
	}
	if t.Version == "" {
		fail("version is required")
	}
	if len(t.VerifyCommand) == 0 || t.VerifyCommand[0] == "" {
		fail("verify_command is required")
	}
	if t.VerifyPattern != "" && !strings.Contains(t.VerifyPattern, VersionPlaceholder) {
		fail("verify_pattern must contain %s", VersionPlaceholder)
	}
	for _, p := range t.CleanupPaths {
		if !check.IsLocalPath(p) {
			fail("cleanup_paths entry %q must be a relative path inside the home directory", p)
		}
	}
	if len(t.Platforms) == 0 {
		fail("at least one platform is required")
	}
	for _, key := range slices.Sorted(maps.Keys(t.Platforms)) {
		if _, err := ParseKey(key); err != nil {
			fail("%v", err)
			continue
		}
		for _, err := range t.Platforms[key].problems() {
			fail("%s: %v", key, err)
		}
	}
	return errors.Join(errs...)
}

func (a Asset) problems() []error {
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if a.URL == "" {
		fail("url is required")
	}
	if !a.ArchiveFormat.Valid() {
		fail("archive_format %q is not one of %v", a.ArchiveFormat, Formats())
	}
	if !check.IsPlainName(a.BinaryName) {
		fail("binary_name %q must be a plain file name", a.BinaryName)
	}
	if !check.IsSHA256(a.SHA256) {
		fail("sha256 must be 64 lowercase hex characters")
	}
	if a.BinaryPath != "" && !check.IsLocalPath(a.BinaryPath) {
		fail("binary_path %q must be a relative path inside the archive", a.BinaryPath)
	}
	for _, extra := range a.ExtraBinaries {
		if !check.IsLocalPath(extra) {
			fail("extra_binaries entry %q must be a relative path inside the archive", extra)
		}
	}

	switch a.ArchiveFormat {
	case FormatSDK:
		if !check.IsLocalPath(a.SDKDir) {
			fail("sdk_dir %q must be a relative path inside the archive", a.SDKDir)
		}
	case FormatBinary:
		if a.BinaryPath != "" || len(a.ExtraBinaries) > 0 {
			fail("binary_path and extra_binaries do not apply to archive_format %q", FormatBinary)
		}
	default:
		// Archive formats accept every field checked above.
	}
	if a.SDKDir != "" && a.ArchiveFormat != FormatSDK {
		fail("sdk_dir only applies to archive_format %q", FormatSDK)
	}
	return errs
}
