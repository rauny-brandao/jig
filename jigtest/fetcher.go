// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jigtest

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/fetch"
)

// Fetcher serves canned responses by URL, writing them into a jig.FS. It
// does not verify checksums itself, so jig reads the file back and verifies
// it; wrap it with NewVerifyingFetcher to exercise streaming verification.
type Fetcher struct {
	fs     jig.FS
	mu     sync.Mutex
	bodies map[string][]byte
	errs   map[string]error
	calls  []string
}

var _ jig.Fetcher = (*Fetcher)(nil)

// NewFetcher returns a Fetcher that writes downloads into fsys.
func NewFetcher(fsys jig.FS) *Fetcher {
	return &Fetcher{fs: fsys, bodies: map[string][]byte{}, errs: map[string]error{}}
}

// Serve makes url return body.
func (f *Fetcher) Serve(url string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies[url] = slices.Clone(body)
}

// Fail makes url return err.
func (f *Fetcher) Fail(url string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[url] = err
}

// Calls returns the URLs requested so far, in order.
func (f *Fetcher) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// Download implements jig.Fetcher.
func (f *Fetcher) Download(ctx context.Context, url, dest string) error {
	body, err := f.respond(ctx, url)
	if err != nil {
		return err
	}
	return f.fs.WriteFile(dest, body, 0o644)
}

func (f *Fetcher) respond(ctx context.Context, url string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, url)
	if err := f.errs[url]; err != nil {
		return nil, err
	}
	body, ok := f.bodies[url]
	if !ok {
		return nil, fmt.Errorf("jigtest: no response registered for %s", url)
	}
	return slices.Clone(body), nil
}

// VerifyingFetcher is a Fetcher that verifies checksums while downloading,
// like fetch.Client.
type VerifyingFetcher struct {
	*Fetcher
}

var _ jig.VerifyingFetcher = VerifyingFetcher{}

// NewVerifyingFetcher returns a VerifyingFetcher that writes into fsys.
func NewVerifyingFetcher(fsys jig.FS) VerifyingFetcher {
	return VerifyingFetcher{Fetcher: NewFetcher(fsys)}
}

// DownloadVerified implements jig.VerifyingFetcher. Nothing is written when
// the checksum does not match.
func (f VerifyingFetcher) DownloadVerified(ctx context.Context, url, dest, sha256 string) error {
	body, err := f.respond(ctx, url)
	if err != nil {
		return err
	}
	if err := fetch.VerifySHA256(body, sha256); err != nil {
		return err
	}
	return f.fs.WriteFile(dest, body, 0o644)
}
