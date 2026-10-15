// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package fetch downloads tool assets over HTTP and verifies their SHA-256
// checksums.
package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rauny-brandao/jig/manifest"
)

// ErrChecksumMismatch means downloaded content does not match the expected
// SHA-256.
var ErrChecksumMismatch = errors.New("checksum mismatch")

// Client downloads files over HTTP. The zero value uses http.DefaultClient.
type Client struct {
	// HTTP is the client used for requests; nil means http.DefaultClient.
	// Bound large downloads with the request context rather than a client
	// timeout.
	HTTP *http.Client
	// UserAgent is sent with every request; empty means "jig".
	UserAgent string
}

// New returns a Client that uses http.DefaultClient.
func New() *Client { return &Client{} }

// Download saves url to dest without verifying its content. dest is only
// replaced once the download completes.
func (c *Client) Download(ctx context.Context, url, dest string) error {
	return c.download(ctx, url, dest, "")
}

// DownloadVerified saves url to dest, hashing while it streams. dest is only
// created when the SHA-256 matches wantSHA256; otherwise the error wraps
// ErrChecksumMismatch and nothing is left behind.
func (c *Client) DownloadVerified(ctx context.Context, url, dest, wantSHA256 string) error {
	if !manifest.IsSHA256(wantSHA256) {
		return fmt.Errorf("download %s: expected sha256 %q is not 64 lowercase hex characters", url, wantSHA256)
	}
	return c.download(ctx, url, dest, wantSHA256)
}

func (c *Client) download(ctx context.Context, url, dest, wantSHA256 string) (err error) {
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("download %s: create directory: %w", url, err)
	}

	tmp, err := os.CreateTemp(dir, ".jig-download-*")
	if err != nil {
		return fmt.Errorf("download %s: create temp file: %w", url, err)
	}
	defer func() {
		_ = tmp.Close()
		if err != nil {
			_ = os.Remove(tmp.Name())
		}
	}()

	hasher := sha256.New()
	if err := c.stream(ctx, url, io.MultiWriter(tmp, hasher)); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("download %s: close temp file: %w", url, err)
	}
	if wantSHA256 != "" {
		if err := matchDigest(hasher, wantSHA256); err != nil {
			return fmt.Errorf("download %s: %w", url, err)
		}
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		return fmt.Errorf("download %s: move into place: %w", url, err)
	}
	return nil
}

// stream writes the body of a GET request for url to w.
func (c *Client) stream(ctx context.Context, url string, w io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	userAgent := c.UserAgent
	if userAgent == "" {
		userAgent = "jig"
	}
	req.Header.Set("User-Agent", userAgent)

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	return nil
}

// HashURL downloads url without saving it and returns the hex-encoded
// SHA-256 of its content.
func (c *Client) HashURL(ctx context.Context, url string) (string, error) {
	hasher := sha256.New()
	if err := c.stream(ctx, url, hasher); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// VerifySHA256 checks data against the expected hex-encoded SHA-256 and
// returns an error wrapping ErrChecksumMismatch when they differ.
func VerifySHA256(data []byte, wantSHA256 string) error {
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, wantSHA256, got)
	}
	return nil
}

func matchDigest(hasher hash.Hash, wantSHA256 string) error {
	if got := hex.EncodeToString(hasher.Sum(nil)); got != wantSHA256 {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, wantSHA256, got)
	}
	return nil
}
