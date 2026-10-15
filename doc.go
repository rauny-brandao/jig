// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package jig installs pinned, checksum-verified developer tools from a
// declarative manifest, plus the extras those tools manage (CLI extensions,
// language runtimes, global packages).
//
// jig is a library: it does no I/O of its own beyond the small interfaces a
// caller provides, so it can be embedded in other CLIs and tested with
// in-memory fakes.
package jig
