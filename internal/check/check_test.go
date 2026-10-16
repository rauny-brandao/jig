// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package check_test

import (
	"strings"
	"testing"

	"github.com/rauny-brandao/jig/internal/check"
)

func TestIsLocalPath(t *testing.T) {
	for p, want := range map[string]bool{
		"bin/tool": true, "tool": true, "a/../b": true, "./tool": true,
		"": false, ".": false, "..": false, "../x": false, "a/../../x": false,
		"/etc/passwd": false, `C:\tool`: false, "C:/tool": false, `bin\tool`: false,
	} {
		if got := check.IsLocalPath(p); got != want {
			t.Errorf("IsLocalPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsPlainName(t *testing.T) {
	for s, want := range map[string]bool{
		"gh": true, "gh.exe": true, ".hidden": true,
		"": false, ".": false, "..": false, "bin/gh": false, `bin\gh`: false, "C:gh": false,
	} {
		if got := check.IsPlainName(s); got != want {
			t.Errorf("IsPlainName(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestIsSHA256(t *testing.T) {
	valid := strings.Repeat("ab12", 16)
	for s, want := range map[string]bool{
		valid: true, strings.ToUpper(valid): false, valid[1:]: false, valid + "0": false, strings.Repeat("g", 64): false,
	} {
		if got := check.IsSHA256(s); got != want {
			t.Errorf("IsSHA256(%q) = %v, want %v", s, got, want)
		}
	}
}
