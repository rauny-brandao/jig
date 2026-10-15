// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package host provides jig's dependencies backed by the real operating
// system: the local filesystem, os/exec, HTTP downloads and archive
// extraction.
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/archive"
	"github.com/rauny-brandao/jig/fetch"
)

// Deps returns jig.Deps backed by the operating system.
func Deps() jig.Deps {
	return jig.Deps{
		FS:        FS{},
		Fetcher:   fetch.New(),
		Extractor: archive.Extractor{},
		Runner:    Runner{},
		Getenv:    os.Getenv,
	}
}

var (
	_ jig.VerifyingFetcher = (*fetch.Client)(nil)
	_ jig.Extractor        = archive.Extractor{}
)

// FS is the local filesystem. The zero value is ready to use.
type FS struct{}

var _ jig.FS = FS{}

// ReadFile reads the named file.
func (FS) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

// WriteFile writes data to the named file, creating or truncating it.
func (FS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

// MkdirAll creates path and any missing parents.
func (FS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// Exists reports whether path exists. A symlink counts even when its target
// is missing.
func (FS) Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Remove deletes path and everything below it; a missing path is not an
// error. See RemoveAll for how it handles files in use on Windows.
func (FS) Remove(path string) error { return RemoveAll(path) }

// Rename moves oldpath to newpath, replacing an existing file. See Rename
// for how it handles files in use on Windows.
func (FS) Rename(oldpath, newpath string) error { return Rename(oldpath, newpath) }

// Chmod changes the mode of the named file.
func (FS) Chmod(name string, mode fs.FileMode) error { return os.Chmod(name, mode) }

// ErrCommandNotFound means the command does not exist.
var ErrCommandNotFound = errors.New("command not found")

// Runner runs commands with os/exec. The zero value is ready to use.
type Runner struct {
	// Env, when set, replaces the inherited environment.
	Env []string
}

var _ jig.Runner = Runner{}

// Run executes name with args and captures its output. A command that exits
// non-zero returns its result and a nil error. On Windows, .cmd and .bat
// scripts run through cmd.exe because they cannot be started directly.
func (r Runner) Run(ctx context.Context, name string, args ...string) (jig.CommandResult, error) {
	return r.RunWithEnv(ctx, nil, name, args...)
}

// RunWithEnv is Run with env ("KEY=value") added to the environment.
func (r Runner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) (jig.CommandResult, error) {
	name, args = wrapBatch(runtime.GOOS, name, args)
	cmd := exec.CommandContext(ctx, name, args...)
	base := r.Env
	if base == nil {
		base = os.Environ()
	}
	cmd.Env = append(slices.Clip(base), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := jig.CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err == nil {
		return result, nil
	}
	if _, ok := errors.AsType[*exec.ExitError](err); ok {
		return result, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return result, fmt.Errorf("run %s: %w", name, ErrCommandNotFound)
	}
	return result, fmt.Errorf("run %s: %w", name, err)
}

func wrapBatch(goos, name string, args []string) (string, []string) {
	if goos != "windows" {
		return name, args
	}
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".cmd") && !strings.HasSuffix(lower, ".bat") {
		return name, args
	}
	return "cmd.exe", append([]string{"/c", name}, args...)
}
