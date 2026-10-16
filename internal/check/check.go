// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package check holds the string checks jig's packages share. It depends only
// on the standard library, so package manifest can use it.
package check

import (
	"path"
	"strings"
)

// IsSHA256 reports whether s is a lowercase hex-encoded SHA-256 digest.
func IsSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// IsPlainName reports whether s is a single file name: not empty, not "." or
// "..", and free of path separators.
func IsPlainName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, `/\:`)
}

// IsLocalPath reports whether p is a slash-separated relative path that stays
// inside its root on every operating system: not empty, not absolute, no
// backslashes or drive letters, and no ".." segment escaping the root.
func IsLocalPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.ContainsAny(p, `\:`) {
		return false
	}
	cleaned := path.Clean(p)
	return cleaned != "." && cleaned != ".." && !strings.HasPrefix(cleaned, "../")
}
