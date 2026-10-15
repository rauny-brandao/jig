// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package checksums_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rauny-brandao/jig/checksums"
)

const _input = `# Pinned tools.
tools:
  gh:
    version: "2.1.0" # bumped by a bot
    platforms:
      linux_amd64:
        url: "https://example.com/gh/{version}/linux.tar.gz"
        archive_format: tar.gz
        binary_name: gh
        sha256: "stale"
      darwin_arm64:
        url: "https://example.com/gh/{version}/darwin.zip"
        archive_format: zip
        binary_name: gh
    verify_command: ["gh", "version"]
  jq:
    version: "1.8.1"
    platforms:
      linux_amd64:
        url: "https://example.com/jq/{version}/jq"
        archive_format: binary
        binary_name: jq
        sha256: "SUM(https://example.com/jq/1.8.1/jq)"
extras:
  - kind: anything
`

// fakeHash returns a predictable "checksum" naming the URL it was asked for.
func fakeHash(_ context.Context, url string) (string, error) {
	return "SUM(" + url + ")", nil
}

func TestUpdateRewritesChangedChecksums(t *testing.T) {
	out, changes, err := checksums.Update(t.Context(), []byte(_input), fakeHash, checksums.Options{})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	var got []string
	for _, c := range changes {
		got = append(got, c.Tool+"/"+c.Platform+": "+c.Old+" -> "+c.New)
	}
	want := []string{
		"gh/linux_amd64: stale -> SUM(https://example.com/gh/2.1.0/linux.tar.gz)",
		"gh/darwin_arm64:  -> SUM(https://example.com/gh/2.1.0/darwin.zip)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	text := string(out)
	for _, keep := range []string{"# Pinned tools.", "# bumped by a bot", "kind: anything", `url: "https://example.com/gh/{version}/linux.tar.gz"`} {
		if !strings.Contains(text, keep) {
			t.Errorf("output lost %q:\n%s", keep, text)
		}
	}
	for _, sum := range []string{
		`sha256: "SUM(https://example.com/gh/2.1.0/linux.tar.gz)"`,
		`sha256: "SUM(https://example.com/gh/2.1.0/darwin.zip)"`,
	} {
		if !strings.Contains(text, sum) {
			t.Errorf("output missing %s:\n%s", sum, text)
		}
	}
}

func TestUpdateLeavesAnUpToDateDocumentUntouched(t *testing.T) {
	input := []byte("tools:\n  jq:\n    version: '1'\n    platforms:\n      linux_amd64: {url: u, sha256: 'SUM(u)'}\n")

	out, changes, err := checksums.Update(t.Context(), input, fakeHash, checksums.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 || string(out) != string(input) {
		t.Errorf("changes = %v, output changed = %v", changes, string(out) != string(input))
	}
}

func TestUpdateOnlyHashesSelectedTools(t *testing.T) {
	var mu sync.Mutex
	var hashed []string
	hash := func(ctx context.Context, url string) (string, error) {
		mu.Lock()
		hashed = append(hashed, url)
		mu.Unlock()
		return fakeHash(ctx, url)
	}

	_, _, err := checksums.Update(t.Context(), []byte(_input), hash, checksums.Options{Tools: []string{"jq"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hashed, []string{"https://example.com/jq/1.8.1/jq"}) {
		t.Errorf("hashed %v, want only jq", hashed)
	}
}

func TestUpdateReportsDownloadFailures(t *testing.T) {
	hash := func(_ context.Context, url string) (string, error) {
		if strings.Contains(url, "darwin") {
			return "", errors.New("HTTP 404")
		}
		return "x", nil
	}

	_, _, err := checksums.Update(t.Context(), []byte(_input), hash, checksums.Options{Parallelism: 1})
	if err == nil || !strings.Contains(err.Error(), "gh/darwin_arm64") || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("Update() error = %v, want the failing asset named", err)
	}
}

func TestUpdateRejectsDocumentsWithoutTools(t *testing.T) {
	for _, input := range []string{"", "other: true\n", "tools: [\n"} {
		if _, _, err := checksums.Update(t.Context(), []byte(input), fakeHash, checksums.Options{}); err == nil {
			t.Errorf("Update(%q) succeeded, want error", input)
		}
	}
}

func TestUpdateWithRealDigests(t *testing.T) {
	sha := func(_ context.Context, url string) (string, error) {
		sum := sha256.Sum256([]byte(url))
		return hex.EncodeToString(sum[:]), nil
	}
	out, changes, err := checksums.Update(t.Context(), []byte(_input), sha, checksums.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	if !strings.Contains(string(out), changes[0].New) {
		t.Error("digest not written")
	}
}
