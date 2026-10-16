// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jigtest

import (
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"sync"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

// File is one file inside a fake archive. Mode 0 means 0644.
type File struct {
	Data []byte
	Mode fs.FileMode
}

// ExtractCall records one call to Extractor.Extract.
type ExtractCall struct {
	ArchivePath string
	Format      manifest.Format
	DestDir     string
}

// Extractor unpacks fake archives. An archive is identified by its exact
// bytes: Register says which files those bytes unpack to.
type Extractor struct {
	fs    jig.FS
	mu    sync.Mutex
	trees map[string]map[string]File
	calls []ExtractCall
}

var _ jig.Extractor = (*Extractor)(nil)

// NewExtractor returns an Extractor that reads and writes fsys.
func NewExtractor(fsys jig.FS) *Extractor {
	return &Extractor{fs: fsys, trees: map[string]map[string]File{}}
}

// Register makes an archive with content archive unpack to files, keyed by
// slash-separated path.
func (e *Extractor) Register(archive []byte, files map[string]File) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.trees[string(archive)] = maps.Clone(files)
}

// Calls returns the extractions performed so far, in order.
func (e *Extractor) Calls() []ExtractCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

// Extract implements jig.Extractor.
func (e *Extractor) Extract(archivePath string, format manifest.Format, destDir string) ([]string, error) {
	data, err := e.fs.ReadFile(archivePath)
	if err != nil {
		return nil, fmt.Errorf("jigtest: read archive: %w", err)
	}
	e.mu.Lock()
	e.calls = append(e.calls, ExtractCall{ArchivePath: archivePath, Format: format, DestDir: destDir})
	tree, ok := e.trees[string(data)]
	e.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("jigtest: no archive registered for content of %s", archivePath)
	}

	created := make([]string, 0, len(tree))
	for _, name := range slices.Sorted(maps.Keys(tree)) {
		dest := filepath.Join(destDir, filepath.FromSlash(name))
		if err := e.fs.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("jigtest: %w", err)
		}
		if err := e.fs.WriteFile(dest, tree[name].Data, tree[name].Mode); err != nil {
			return nil, fmt.Errorf("jigtest: %w", err)
		}
		created = append(created, dest)
	}
	return created, nil
}
