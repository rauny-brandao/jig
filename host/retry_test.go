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

var (
	errLocked = errors.New("locked")
	errOther  = errors.New("other")
)

func isLocked(err error) bool { return errors.Is(err, errLocked) }

func shortRetry(t *testing.T, d time.Duration) {
	t.Helper()
	old := retryFor
	retryFor = d
	t.Cleanup(func() { retryFor = old })
}

func TestRetry(t *testing.T) {
	shortRetry(t, time.Second)
	tests := []struct {
		name      string
		errs      []error // returned by successive calls; nil after the last
		wantErr   error
		wantCalls int
	}{
		{"succeeds at once", nil, nil, 1},
		{"retries while locked", []error{errLocked, errLocked}, nil, 3},
		{"returns other errors at once", []error{errOther}, errOther, 1},
		{"stops at another error", []error{errLocked, errOther}, errOther, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := retry(func() error {
				calls++
				if calls <= len(tt.errs) {
					return tt.errs[calls-1]
				}
				return nil
			}, isLocked)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("retry() error = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
		})
	}
}

func TestRetryGivesUpAfterRetryFor(t *testing.T) {
	shortRetry(t, 100*time.Millisecond)
	start := time.Now()
	err := retry(func() error { return errLocked }, isLocked)
	if !errors.Is(err, errLocked) {
		t.Fatalf("retry() error = %v, want the last error", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond || elapsed > time.Second {
		t.Errorf("retry() gave up after %v, want about 100ms", elapsed)
	}
}

func TestRemoveAllAndRename(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Rename(src, dst); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	if err := RemoveAll(dst); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists after RemoveAll: %v", dst, err)
	}
	if err := RemoveAll(dst); err != nil {
		t.Errorf("RemoveAll() of a missing path = %v, want nil", err)
	}
	start := time.Now()
	if err := Rename(src, dst); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Rename() of a missing file = %v, want ErrNotExist", err)
	}
	if elapsed := time.Since(start); elapsed > retryFor/2 {
		t.Errorf("Rename() of a missing file took %v; it should not retry", elapsed)
	}
}
