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
//
// Status reports the State of every item without changing the machine. Plan
// turns statuses into actions: missing items are installed and outdated ones
// upgraded, while items newer than their pin are left alone unless
// PlanOptions.Downgrade selects them. A Filter such as Named or OfKind
// narrows a plan to some items. Apply carries the plan out in dependency
// order.
//
// WithRecord keeps a record of the items jig manages, which Apply updates.
// PlanRemoveRecorded uses it to remove items the manifest no longer declares,
// or everything, without asking the tools that own them.
package jig
