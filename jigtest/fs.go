// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jigtest

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/rauny-brandao/jig"
)

// FS is an in-memory filesystem. Like a real one, it refuses to write into a
// directory that does not exist, so a missing MkdirAll fails the test.
type FS struct {
	mu    sync.Mutex
	nodes map[string]*node
}

type node struct {
	dir  bool
	data []byte
	mode fs.FileMode
}

var _ jig.FS = (*FS)(nil)

// NewFS returns an empty filesystem.
func NewFS() *FS { return &FS{nodes: map[string]*node{}} }

// Seed creates a file and its parent directories. Mode 0 means 0644.
func (f *FS) Seed(path string, data []byte, mode fs.FileMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = filepath.Clean(path)
	_ = f.mkdirAll(filepath.Dir(path))
	f.nodes[path] = &node{data: slices.Clone(data), mode: fileMode(mode)}
}

// File returns a file's content and mode.
func (f *FS) File(path string) ([]byte, fs.FileMode, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[filepath.Clean(path)]
	if !ok || n.dir {
		return nil, 0, false
	}
	return slices.Clone(n.data), n.mode, true
}

// IsDir reports whether path is a directory.
func (f *FS) IsDir(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.nodes[filepath.Clean(path)]
	return ok && n.dir
}

// Files returns the sorted paths of all files at or below root.
func (f *FS) Files(root string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	root = filepath.Clean(root)
	var files []string
	for path, n := range f.nodes {
		if !n.dir && (path == root || isBelow(path, root)) {
			files = append(files, path)
		}
	}
	slices.Sort(files)
	return files
}

// ReadFile implements jig.FS.
func (f *FS) ReadFile(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name = filepath.Clean(name)
	n, ok := f.nodes[name]
	switch {
	case !ok:
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	case n.dir:
		return nil, &fs.PathError{Op: "read", Path: name, Err: errIsDir}
	}
	return slices.Clone(n.data), nil
}

// WriteFile implements jig.FS.
func (f *FS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	name = filepath.Clean(name)
	if !f.dirExists(filepath.Dir(name)) {
		return &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if n, ok := f.nodes[name]; ok && n.dir {
		return &fs.PathError{Op: "open", Path: name, Err: errIsDir}
	}
	f.nodes[name] = &node{data: slices.Clone(data), mode: fileMode(perm)}
	return nil
}

// MkdirAll implements jig.FS.
func (f *FS) MkdirAll(path string, _ fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mkdirAll(filepath.Clean(path))
}

// Exists implements jig.FS.
func (f *FS) Exists(path string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = filepath.Clean(path)
	_, ok := f.nodes[path]
	return ok || isRoot(path)
}

// Remove implements jig.FS.
func (f *FS) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	path = filepath.Clean(path)
	maps.DeleteFunc(f.nodes, func(p string, _ *node) bool { return p == path || isBelow(p, path) })
	return nil
}

// Rename implements jig.FS. Like rename(2), it replaces an existing file but
// refuses to replace a directory.
func (f *FS) Rename(oldpath, newpath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	oldpath, newpath = filepath.Clean(oldpath), filepath.Clean(newpath)
	src, ok := f.nodes[oldpath]
	if !ok {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrNotExist}
	}
	if !f.dirExists(filepath.Dir(newpath)) {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrNotExist}
	}
	if dst, ok := f.nodes[newpath]; ok && (dst.dir || src.dir) {
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: fs.ErrExist}
	}
	moved := map[string]*node{}
	for p, n := range f.nodes {
		if p == oldpath {
			moved[newpath] = n
		} else if isBelow(p, oldpath) {
			moved[newpath+p[len(oldpath):]] = n
		}
	}
	maps.DeleteFunc(f.nodes, func(p string, _ *node) bool { return p == oldpath || isBelow(p, oldpath) })
	maps.Copy(f.nodes, moved)
	return nil
}

// Chmod implements jig.FS.
func (f *FS) Chmod(name string, mode fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	name = filepath.Clean(name)
	n, ok := f.nodes[name]
	if !ok {
		return &fs.PathError{Op: "chmod", Path: name, Err: fs.ErrNotExist}
	}
	n.mode = mode.Perm()
	return nil
}

var (
	errIsDir         = errors.New("is a directory")
	errNotADir       = errors.New("not a directory")
	_defaultFileMode = fs.FileMode(0o644)
)

func (f *FS) mkdirAll(path string) error {
	for p := path; !isRoot(p); p = filepath.Dir(p) {
		if n, ok := f.nodes[p]; ok {
			if !n.dir {
				return &fs.PathError{Op: "mkdir", Path: p, Err: errNotADir}
			}
			continue
		}
		f.nodes[p] = &node{dir: true, mode: fs.ModeDir | 0o755}
	}
	return nil
}

func (f *FS) dirExists(path string) bool {
	if isRoot(path) {
		return true
	}
	n, ok := f.nodes[path]
	return ok && n.dir
}

func isRoot(path string) bool { return filepath.Dir(path) == path }

func isBelow(path, root string) bool {
	if isRoot(root) {
		return path != root
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func fileMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm() == 0 {
		return _defaultFileMode
	}
	return mode.Perm()
}
