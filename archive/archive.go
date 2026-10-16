// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package archive unpacks downloaded tool assets into a directory. Every
// entry, symlink target and hard-link target must stay inside that
// directory; anything else is rejected with ErrUnsafePath.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rauny-brandao/jig/manifest"
)

// ErrUnsafePath means an archive entry would be written outside the
// destination directory.
var ErrUnsafePath = errors.New("archive entry escapes destination")

// Extractor unpacks archives on the local filesystem. The zero value is ready
// to use.
type Extractor struct{}

// Extract unpacks archivePath into destDir according to format and returns
// the paths of the files and links it created. Supported formats are zip,
// tar.gz, tar.bz2, gz (a single compressed file) and binary (copied as is).
func (Extractor) Extract(archivePath string, format manifest.Format, destDir string) ([]string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, fmt.Errorf("extract %s: create destination: %w", archivePath, err)
	}
	var (
		created []string
		err     error
	)
	switch format {
	case manifest.FormatZip:
		created, err = extractZip(archivePath, destDir)
	case manifest.FormatTarGz:
		created, err = extractCompressedTar(archivePath, destDir, func(r io.Reader) (io.Reader, error) { return gzip.NewReader(r) })
	case manifest.FormatTarBz2:
		created, err = extractCompressedTar(archivePath, destDir, func(r io.Reader) (io.Reader, error) { return bzip2.NewReader(r), nil })
	case manifest.FormatGz:
		created, err = extractGz(archivePath, destDir)
	case manifest.FormatBinary:
		created, err = copyBinary(archivePath, destDir)
	case manifest.FormatSDK:
		err = fmt.Errorf("format %q must be extracted as zip or tar.gz", format)
	default:
		err = fmt.Errorf("unsupported format %q", format)
	}
	if err != nil {
		return nil, fmt.Errorf("extract %s: %w", archivePath, err)
	}
	return created, nil
}

func extractZip(archivePath, destDir string) ([]string, error) {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer func() { _ = r.Close() }()

	var created []string
	for _, f := range r.File {
		target, err := entryPath(destDir, f.Name)
		if err != nil {
			return nil, err
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("create directory %s: %w", target, err)
			}
		case mode&fs.ModeSymlink != 0:
			linkTarget, err := readZipLink(f)
			if err != nil {
				return nil, err
			}
			if err := createSymlink(f.Name, linkTarget, target); err != nil {
				return nil, err
			}
			created = append(created, target)
		default:
			if err := writeZipFile(f, target, mode); err != nil {
				return nil, err
			}
			created = append(created, target)
		}
	}
	return created, nil
}

func readZipLink(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	target, err := io.ReadAll(io.LimitReader(rc, 4096))
	if err != nil {
		return "", fmt.Errorf("read zip symlink %s: %w", f.Name, err)
	}
	return string(target), nil
}

func writeZipFile(f *zip.File, target string, mode fs.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	return writeFile(target, rc, mode)
}

func extractCompressedTar(archivePath, destDir string, decompress func(io.Reader) (io.Reader, error)) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	r, err := decompress(f)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	return extractTar(tar.NewReader(r), destDir)
}

func extractTar(tr *tar.Reader, destDir string) ([]string, error) {
	var created []string
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return created, nil
		}
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		target, err := entryPath(destDir, header.Name)
		if err != nil {
			return nil, err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("create directory %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := writeFile(target, tr, header.FileInfo().Mode()); err != nil {
				return nil, err
			}
			created = append(created, target)
		case tar.TypeSymlink:
			if err := createSymlink(header.Name, header.Linkname, target); err != nil {
				return nil, err
			}
			created = append(created, target)
		case tar.TypeLink:
			source, err := entryPath(destDir, header.Linkname)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return nil, fmt.Errorf("create directory for %s: %w", target, err)
			}
			_ = os.Remove(target)
			if err := os.Link(source, target); err != nil {
				return nil, fmt.Errorf("create hard link %s: %w", target, err)
			}
			created = append(created, target)
		}
		// Devices, FIFOs and other special entries are never needed to run
		// a tool, so they are skipped.
	}
}

// extractGz decompresses a single gzip-compressed file. It is named after
// the gzip header when present, otherwise after the archive minus ".gz".
func extractGz(archivePath, destDir string) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("decompress: %w", err)
	}
	defer func() { _ = gr.Close() }()

	name := strings.TrimSuffix(filepath.Base(archivePath), ".gz")
	if gr.Name != "" {
		name = filepath.Base(filepath.FromSlash(gr.Name))
	}
	target, err := entryPath(destDir, name)
	if err != nil {
		return nil, err
	}
	if err := writeFile(target, gr, 0o755); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

func copyBinary(archivePath, destDir string) ([]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open binary: %w", err)
	}
	defer func() { _ = f.Close() }()

	target := filepath.Join(destDir, filepath.Base(archivePath))
	if err := writeFile(target, f, 0o755); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

// entryPath joins a slash-separated archive entry name onto destDir,
// rejecting names that would land outside it.
func entryPath(destDir, name string) (string, error) {
	rel := filepath.FromSlash(name)
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	return filepath.Join(destDir, rel), nil
}

// createSymlink creates the link at target for archive entry name. The link
// target must resolve, relative to the entry's directory, to a path inside
// the archive root, so later entries can never be written through it to the
// outside.
func createSymlink(name, linkTarget, target string) error {
	if linkTarget == "" || filepath.IsAbs(linkTarget) || strings.HasPrefix(linkTarget, "/") ||
		strings.HasPrefix(linkTarget, `\`) || strings.Contains(linkTarget, ":") {
		return fmt.Errorf("%w: symlink %q points to %q", ErrUnsafePath, name, linkTarget)
	}
	resolved := filepath.Join(filepath.Dir(filepath.FromSlash(name)), filepath.FromSlash(linkTarget))
	if !filepath.IsLocal(resolved) {
		return fmt.Errorf("%w: symlink %q points to %q", ErrUnsafePath, name, linkTarget)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", target, err)
	}
	_ = os.Remove(target)
	if err := os.Symlink(filepath.FromSlash(linkTarget), target); err != nil {
		return fmt.Errorf("create symlink %s: %w", target, err)
	}
	return nil
}

// writeFile writes r to path with mode's permission bits (0644 when none are
// set), replacing any existing file or link.
func writeFile(path string, r io.Reader, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory for %s: %w", path, err)
	}
	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	_ = os.Remove(path)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := io.Copy(out, r); err != nil {
		_ = out.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}
