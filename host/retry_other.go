// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !windows

package host

// inUse reports whether err means another process has the file open. Other
// systems let a file be removed or replaced while it is open.
func inUse(error) bool { return false }
