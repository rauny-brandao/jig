// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const _manifest = `tools:
  jq:
    version: "1.8.1"
    platforms:
      linux_amd64:
        url: "https://example.com/jq/{version}/jq"
        sha256: "old"
`

func hashOf(_ context.Context, url string) (string, error) { return "new:" + url, nil }

func writeManifest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tools.yaml")
	if err := os.WriteFile(path, []byte(_manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunUpdatesTheFile(t *testing.T) {
	path := writeManifest(t)
	var stderr bytes.Buffer

	if err := run(t.Context(), []string{"-f", path}, &stderr, hashOf); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `sha256: "new:https://example.com/jq/1.8.1/jq"`) {
		t.Errorf("manifest not updated:\n%s", data)
	}
	if !strings.Contains(stderr.String(), "jq/linux_amd64: old -> new:") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 && os.PathSeparator == '/' {
		t.Errorf("permissions changed to %v", info.Mode().Perm())
	}
}

func TestRunCheckReportsWithoutWriting(t *testing.T) {
	path := writeManifest(t)
	var stderr bytes.Buffer

	err := run(t.Context(), []string{"-f", path, "-check"}, &stderr, hashOf)

	if !errors.Is(err, errChecksumsDiffer) {
		t.Fatalf("run() error = %v, want errChecksumsDiffer", err)
	}
	if data, _ := os.ReadFile(path); string(data) != _manifest {
		t.Error("-check modified the manifest")
	}
}

func TestRunReportsUpToDate(t *testing.T) {
	path := writeManifest(t)
	same := func(context.Context, string) (string, error) { return "old", nil }
	var stderr bytes.Buffer

	if err := run(t.Context(), []string{"-f", path, "-check"}, &stderr, same); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(stderr.String(), "up to date") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRunRejectsAMissingFile(t *testing.T) {
	err := run(t.Context(), []string{"-f", filepath.Join(t.TempDir(), "missing.yaml")}, &bytes.Buffer{}, hashOf)
	if err == nil {
		t.Fatal("run() succeeded for a missing file")
	}
}
