// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package archive_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/rauny-brandao/jig/archive"
	"github.com/rauny-brandao/jig/manifest"
	"github.com/ulikunitz/xz"
)

type entry struct {
	name     string
	body     string
	mode     int64
	typeflag byte
	link     string
}

func TestExtractZip(t *testing.T) {
	src := writeZip(t, []entry{
		{name: "pkg/", typeflag: tar.TypeDir},
		{name: "pkg/bin/tool", body: "zip tool", mode: 0o755},
		{name: "pkg/README", body: "readme", mode: 0o644},
	})
	dest := t.TempDir()

	created, err := archive.Extractor{}.Extract(src, "zip", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "pkg", "bin", "tool"), "zip tool", 0o755)
	assertFile(t, filepath.Join(dest, "pkg", "README"), "readme", 0o644)
	if len(created) != 2 {
		t.Errorf("created = %v, want 2 files", created)
	}
}

func TestExtractZipSymlinks(t *testing.T) {
	skipWithoutSymlinks(t)
	src := writeZip(t, []entry{
		{name: "pkg/libexec/tool", body: "real", mode: 0o755},
		{name: "pkg/bin/tool", body: "../libexec/tool", typeflag: tar.TypeSymlink},
	})
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "zip", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "pkg", "bin", "tool"), "real", 0o755)

	escaping := writeZip(t, []entry{{name: "link", body: "../../outside", typeflag: tar.TypeSymlink}})
	if _, err := (archive.Extractor{}).Extract(escaping, "zip", t.TempDir()); !errors.Is(err, archive.ErrUnsafePath) {
		t.Errorf("escaping zip symlink: error = %v, want ErrUnsafePath", err)
	}
}

func TestExtractTarGz(t *testing.T) {
	src := writeTarGz(t, []entry{
		{name: "./pkg/", typeflag: tar.TypeDir},
		{name: "./pkg/tool", body: "tar tool", mode: 0o755, typeflag: tar.TypeReg},
		{name: "./pkg/tool-copy", typeflag: tar.TypeLink, link: "pkg/tool"},
	})
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "tar.gz", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "pkg", "tool"), "tar tool", 0o755)
	assertFile(t, filepath.Join(dest, "pkg", "tool-copy"), "tar tool", 0o755)
}

func TestExtractTarGzSymlinkInsideArchive(t *testing.T) {
	skipWithoutSymlinks(t)
	src := writeTarGz(t, []entry{
		{name: "pkg/libexec/tool", body: "real", mode: 0o755, typeflag: tar.TypeReg},
		{name: "pkg/bin/tool", typeflag: tar.TypeSymlink, link: "../libexec/tool"},
	})
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "tar.gz", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "pkg", "bin", "tool"), "real", 0o755)
}

func TestExtractTarBz2(t *testing.T) {
	dest := t.TempDir()
	if _, err := (archive.Extractor{}).Extract(filepath.Join("testdata", "tool.tar.bz2"), "tar.bz2", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "dir", "tool"), "bz2 tool\n", 0o755)
}

func TestExtractTarXz(t *testing.T) {
	src := writeTarXz(t, []entry{
		{name: "pkg/", typeflag: tar.TypeDir},
		{name: "pkg/tool", body: "xz tool", mode: 0o755, typeflag: tar.TypeReg},
	})
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "tar.xz", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "pkg", "tool"), "xz tool", 0o755)
}

func TestExtractGzUsesHeaderName(t *testing.T) {
	src := writeGz(t, "download.gz", "tool-darwin-arm64", "gz tool")
	dest := t.TempDir()

	created, err := archive.Extractor{}.Extract(src, "gz", dest)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	want := filepath.Join(dest, "tool-darwin-arm64")
	if !slices.Equal(created, []string{want}) {
		t.Errorf("created = %v, want %v", created, want)
	}
	assertFile(t, want, "gz tool", 0o755)
}

func TestExtractGzFallsBackToArchiveName(t *testing.T) {
	src := writeGz(t, "tool-linux.gz", "", "gz tool")
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "gz", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "tool-linux"), "gz tool", 0o755)
}

func TestExtractGzIgnoresDirectoriesInHeaderName(t *testing.T) {
	src := writeGz(t, "download.gz", "../../evil", "gz tool")
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "gz", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "evil"), "gz tool", 0o755)
}

func TestExtractBinaryCopiesTheFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(src, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()

	if _, err := (archive.Extractor{}).Extract(src, "binary", dest); err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	assertFile(t, filepath.Join(dest, "tool"), "binary", 0o755)
}

func TestExtractRejectsEntriesOutsideTheDestination(t *testing.T) {
	tests := []struct {
		name    string
		format  manifest.Format
		entries []entry
	}{
		{"zip parent traversal", "zip", []entry{{name: "../evil", body: "x"}}},
		{"zip nested traversal", "zip", []entry{{name: "a/../../evil", body: "x"}}},
		{"tar parent traversal", "tar.gz", []entry{{name: "../evil", body: "x", typeflag: tar.TypeReg}}},
		{"tar absolute path", "tar.gz", []entry{{name: "/tmp/evil", body: "x", typeflag: tar.TypeReg}}},
		{"symlink escaping", "tar.gz", []entry{{name: "link", typeflag: tar.TypeSymlink, link: "../outside"}}},
		{"nested symlink escaping", "tar.gz", []entry{{name: "a/b/link", typeflag: tar.TypeSymlink, link: "../../../outside"}}},
		{"absolute symlink", "tar.gz", []entry{{name: "link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}}},
		{"hard link escaping", "tar.gz", []entry{{name: "link", typeflag: tar.TypeLink, link: "../outside"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var src string
			if tt.format == "zip" {
				src = writeZip(t, tt.entries)
			} else {
				src = writeTarGz(t, tt.entries)
			}
			dest := filepath.Join(t.TempDir(), "dest")

			_, err := archive.Extractor{}.Extract(src, tt.format, dest)
			if !errors.Is(err, archive.ErrUnsafePath) {
				t.Fatalf("Extract() error = %v, want ErrUnsafePath", err)
			}
			if _, err := os.Lstat(filepath.Join(filepath.Dir(dest), "evil")); err == nil {
				t.Error("file was written outside the destination")
			}
		})
	}
}

func TestExtractRejectsUnsupportedFormats(t *testing.T) {
	for _, format := range []manifest.Format{"rar", manifest.FormatSDK} {
		if _, err := (archive.Extractor{}).Extract("unused", format, t.TempDir()); err == nil {
			t.Errorf("Extract(format %q) succeeded, want error", format)
		}
	}
}

func writeZip(t *testing.T, entries []entry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := fs.FileMode(e.mode)
		switch e.typeflag {
		case tar.TypeDir:
			mode = fs.ModeDir | 0o755
		case tar.TypeSymlink:
			mode = fs.ModeSymlink | 0o777
		}
		header.SetMode(mode)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, "archive.zip", buf.Bytes())
}

func writeTarGz(t *testing.T, entries []entry) string {
	t.Helper()
	return writeTar(t, "archive.tar.gz", func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) }, entries)
}

func writeTarXz(t *testing.T, entries []entry) string {
	t.Helper()
	return writeTar(t, "archive.tar.xz", func(w io.Writer) io.WriteCloser {
		xw, err := xz.NewWriter(w)
		if err != nil {
			t.Fatal(err)
		}
		return xw
	}, entries)
}

func writeTar(t *testing.T, name string, compress func(io.Writer) io.WriteCloser, entries []entry) string {
	t.Helper()
	var buf bytes.Buffer
	cw := compress(&buf)
	tw := tar.NewWriter(cw)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typeflag, Linkname: e.link, Size: int64(len(e.body))}
		if e.typeflag != tar.TypeReg {
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Size > 0 {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, name, buf.Bytes())
}

func writeGz(t *testing.T, fileName, headerName, body string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Name = headerName
	if _, err := gw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, fileName, buf.Bytes())
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFile(t *testing.T, path, body string, perm fs.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != body {
		t.Errorf("%s = %q, want %q", path, data, body)
	}
	if runtime.GOOS == "windows" {
		return // Windows has no Unix permission bits.
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != perm {
		t.Errorf("%s mode = %v, want %v", path, info.Mode().Perm(), perm)
	}
}

func skipWithoutSymlinks(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}
