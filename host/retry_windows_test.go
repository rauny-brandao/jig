// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package host

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// lockFile creates a file and keeps it open, which on Windows stops it from
// being removed or replaced. The returned function closes it.
func lockFile(t *testing.T) (string, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.exe")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return path, func() { _ = f.Close() }
}

func TestRemoveAllWaitsForALockedFile(t *testing.T) {
	path, unlock := lockFile(t)
	time.AfterFunc(200*time.Millisecond, unlock)

	if err := RemoveAll(path); err != nil {
		t.Fatalf("RemoveAll() error = %v, want it to succeed once the file is closed", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists: %v", path, err)
	}
}

func TestRenameWaitsForALockedFile(t *testing.T) {
	path, unlock := lockFile(t)
	time.AfterFunc(200*time.Millisecond, unlock)

	if err := Rename(path, path+".old"); err != nil {
		t.Fatalf("Rename() error = %v, want it to succeed once the file is closed", err)
	}
}

func TestRemoveAllGivesUpOnAFileThatStaysLocked(t *testing.T) {
	shortRetry(t, 100*time.Millisecond)
	path, _ := lockFile(t)

	err := RemoveAll(path)
	if err == nil || !inUse(err) {
		t.Fatalf("RemoveAll() error = %v, want the in-use error", err)
	}
}
