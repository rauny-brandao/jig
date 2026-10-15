// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jigtest_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/jigtest"
)

func p(s string) string { return filepath.FromSlash(s) }

func TestFSRequiresParentDirectories(t *testing.T) {
	fsys := jigtest.NewFS()
	if err := fsys.WriteFile(p("/a/b/file"), []byte("x"), 0o644); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteFile without parent = %v, want ErrNotExist", err)
	}
	if err := fsys.MkdirAll(p("/a/b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := fsys.WriteFile(p("/a/b/file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, mode, ok := fsys.File(p("/a/b/file")); !ok || string(data) != "x" || mode != 0o600 {
		t.Errorf("File() = %q, %v, %v", data, mode, ok)
	}
	if !fsys.IsDir(p("/a")) {
		t.Error("parent directory was not created")
	}
}

func TestFSRenameMovesDirectoryTrees(t *testing.T) {
	fsys := jigtest.NewFS()
	fsys.Seed(p("/src/dir/one"), []byte("1"), 0)
	fsys.Seed(p("/src/dir/sub/two"), []byte("2"), 0)
	fsys.Seed(p("/src/dirty"), []byte("not below dir"), 0)
	if err := fsys.MkdirAll(p("/dst"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := fsys.Rename(p("/src/dir"), p("/dst/dir")); err != nil {
		t.Fatal(err)
	}

	want := []string{p("/dst/dir/one"), p("/dst/dir/sub/two")}
	if got := fsys.Files(p("/dst")); !slices.Equal(got, want) {
		t.Errorf("moved files = %v, want %v", got, want)
	}
	if got := fsys.Files(p("/src")); !slices.Equal(got, []string{p("/src/dirty")}) {
		t.Errorf("left behind = %v", got)
	}
}

func TestFSRenameReplacesFilesButNotDirectories(t *testing.T) {
	fsys := jigtest.NewFS()
	fsys.Seed(p("/a"), []byte("new"), 0)
	fsys.Seed(p("/b"), []byte("old"), 0)
	fsys.Seed(p("/dir/x"), []byte("x"), 0)

	if err := fsys.Rename(p("/a"), p("/b")); err != nil {
		t.Fatalf("replace file: %v", err)
	}
	if data, _, _ := fsys.File(p("/b")); string(data) != "new" {
		t.Errorf("/b = %q, want new", data)
	}
	if err := fsys.Rename(p("/b"), p("/dir")); !errors.Is(err, fs.ErrExist) {
		t.Errorf("replace directory = %v, want ErrExist", err)
	}
}

func TestFSRemoveIsRecursiveAndToleratesMissingPaths(t *testing.T) {
	fsys := jigtest.NewFS()
	fsys.Seed(p("/dir/sub/file"), []byte("x"), 0)
	fsys.Seed(p("/dirt"), []byte("sibling"), 0)

	if err := fsys.Remove(p("/dir")); err != nil {
		t.Fatal(err)
	}
	if fsys.Exists(p("/dir/sub/file")) || fsys.Exists(p("/dir")) {
		t.Error("directory tree not removed")
	}
	if !fsys.Exists(p("/dirt")) {
		t.Error("sibling with a shared prefix was removed")
	}
	if err := fsys.Remove(p("/missing")); err != nil {
		t.Errorf("Remove(missing) = %v", err)
	}
}

func TestRunnerReturnsStubsAndRecordsCalls(t *testing.T) {
	r := jigtest.NewRunner()
	r.Stub(jig.CommandResult{Stdout: "1.0.0"}, "tool", "--version")

	got, err := r.Run(t.Context(), "tool", "--version")
	if err != nil || got.Stdout != "1.0.0" {
		t.Errorf("stubbed Run() = %+v, %v", got, err)
	}
	if _, err := r.Run(t.Context(), "tool", "other"); !errors.Is(err, jigtest.ErrCommandNotFound) {
		t.Errorf("unstubbed Run() error = %v, want ErrCommandNotFound", err)
	}
	if calls := r.Calls(); len(calls) != 2 || !slices.Equal(calls[1], []string{"tool", "other"}) {
		t.Errorf("Calls() = %v", calls)
	}
}
