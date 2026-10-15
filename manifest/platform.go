// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package manifest

import (
	"fmt"
	"runtime"
	"strings"
)

// Platform is an operating system and CPU architecture pair, using Go's
// GOOS and GOARCH names.
type Platform struct {
	OS   string
	Arch string
}

// Current returns the platform the program is running on.
func Current() Platform { return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH} }

// Key returns the manifest key for the platform, such as "darwin_arm64".
func (p Platform) Key() string { return p.OS + "_" + p.Arch }

// ParseKey parses a manifest platform key such as "linux_amd64".
func ParseKey(key string) (Platform, error) {
	goos, arch, ok := strings.Cut(key, "_")
	if !ok || goos == "" || arch == "" || strings.Contains(arch, "_") {
		return Platform{}, fmt.Errorf("invalid platform key %q: want <os>_<arch>", key)
	}
	return Platform{OS: goos, Arch: arch}, nil
}
