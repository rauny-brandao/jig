// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package host

import (
	"os"
	"time"
)

// retryFor is how long RemoveAll and Rename keep retrying a file that is in
// use.
var retryFor = 2 * time.Second

// RemoveAll removes path and everything below it, like os.RemoveAll. On
// Windows, a file stays locked for a moment after the process that used it
// exits, or while antivirus scans it, so RemoveAll retries for about two
// seconds while the file is denied or in use.
func RemoveAll(path string) error {
	return retry(func() error { return os.RemoveAll(path) }, inUse)
}

// Rename moves oldpath to newpath, replacing an existing file, like
// os.Rename. It retries on Windows the way RemoveAll does.
func Rename(oldpath, newpath string) error {
	return retry(func() error { return os.Rename(oldpath, newpath) }, inUse)
}

// retry runs op until it succeeds, fails with an error that transient
// rejects, or retryFor has passed.
func retry(op func() error, transient func(error) bool) error {
	err := op()
	deadline := time.Now().Add(retryFor)
	for wait := 10 * time.Millisecond; err != nil && transient(err) && time.Now().Before(deadline); wait = min(2*wait, 250*time.Millisecond) {
		time.Sleep(wait)
		err = op()
	}
	return err
}
