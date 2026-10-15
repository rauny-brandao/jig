// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fetch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rauny-brandao/jig/fetch"
)

const _body = "release asset"

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /asset", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "jig" {
			http.Error(w, "missing user agent", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(_body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestDownloadVerifiedWritesMatchingContent(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "nested", "asset")

	if err := fetch.New().DownloadVerified(t.Context(), srv.URL+"/asset", dest, digest(_body)); err != nil {
		t.Fatalf("DownloadVerified() error = %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != _body {
		t.Fatalf("dest = %q, %v; want %q", data, err, _body)
	}
	assertOnlyFile(t, filepath.Dir(dest), "asset")
}

func TestDownloadVerifiedRejectsMismatchAndLeavesNothing(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "asset")

	err := fetch.New().DownloadVerified(t.Context(), srv.URL+"/asset", dest, digest("something else"))
	if !errors.Is(err, fetch.ErrChecksumMismatch) {
		t.Fatalf("DownloadVerified() error = %v, want ErrChecksumMismatch", err)
	}
	assertOnlyFile(t, filepath.Dir(dest), "")
}

func TestDownloadVerifiedRejectsMalformedChecksumBeforeDownloading(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	t.Cleanup(srv.Close)

	err := fetch.New().DownloadVerified(t.Context(), srv.URL, filepath.Join(t.TempDir(), "asset"), "not-a-sha")
	if err == nil || requests != 0 {
		t.Fatalf("error = %v, requests = %d; want error and no request", err, requests)
	}
}

func TestDownloadReportsHTTPErrors(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "asset")

	err := fetch.New().Download(t.Context(), srv.URL+"/missing", dest)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("Download() error = %v, want 404", err)
	}
	assertOnlyFile(t, filepath.Dir(dest), "")
}

func TestDownloadHonoursContext(t *testing.T) {
	srv := newServer(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := fetch.New().Download(ctx, srv.URL+"/asset", filepath.Join(t.TempDir(), "asset"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download() error = %v, want context.Canceled", err)
	}
}

func TestDownloadReplacesExistingFile(t *testing.T) {
	srv := newServer(t)
	dest := filepath.Join(t.TempDir(), "asset")
	if err := os.WriteFile(dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fetch.New().Download(t.Context(), srv.URL+"/asset", dest); err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if data, _ := os.ReadFile(dest); string(data) != _body {
		t.Errorf("dest = %q, want %q", data, _body)
	}
}

func TestHashURL(t *testing.T) {
	srv := newServer(t)
	got, err := fetch.New().HashURL(t.Context(), srv.URL+"/asset")
	if err != nil || got != digest(_body) {
		t.Fatalf("HashURL() = %q, %v; want %q", got, err, digest(_body))
	}
}

func TestVerifySHA256(t *testing.T) {
	if err := fetch.VerifySHA256([]byte(_body), digest(_body)); err != nil {
		t.Errorf("matching digest: %v", err)
	}
	if err := fetch.VerifySHA256([]byte(_body), digest("x")); !errors.Is(err, fetch.ErrChecksumMismatch) {
		t.Errorf("mismatching digest: %v, want ErrChecksumMismatch", err)
	}
}

// assertOnlyFile checks that dir contains exactly the named file, or nothing
// when name is empty, so no temporary download is left behind.
func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if name == "" && len(names) != 0 || name != "" && (len(names) != 1 || names[0] != name) {
		t.Errorf("%s contains %v, want only %q", dir, names, name)
	}
}
