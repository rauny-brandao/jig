// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"

	"github.com/rauny-brandao/jig/fetch"
	"github.com/rauny-brandao/jig/internal/check"
	"github.com/rauny-brandao/jig/manifest"
)

const (
	_binaryPerm = 0o755
	_dirPerm    = 0o755
)

// installTool downloads, verifies and installs one tool. The download and
// extraction live in a per-tool cache directory that is removed afterwards,
// and each binary is written beside its destination and renamed into place,
// so a failed install never leaves a half-written binary.
func (j *Jig) installTool(ctx context.Context, action Action) error {
	asset := action.Asset
	if !check.IsSHA256(asset.SHA256) {
		return fmt.Errorf("sha256 %q in manifest is not 64 lowercase hex characters", asset.SHA256)
	}

	work := filepath.Join(j.dirs.Cache, action.Name)
	if err := j.removeIfExists(work); err != nil {
		return fmt.Errorf("clear cache %s: %w", work, err)
	}
	for _, dir := range []string{work, j.dirs.Bin} {
		if err := j.deps.FS.MkdirAll(dir, _dirPerm); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	defer func() { _ = j.removeIfExists(work) }()

	download := filepath.Join(work, "download", downloadName(action.Name, asset.URL))
	if err := j.deps.FS.MkdirAll(filepath.Dir(download), _dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(download), err)
	}
	if err := j.download(ctx, asset, download); err != nil {
		return err
	}

	var (
		installedPath string
		err           error
	)
	switch asset.ArchiveFormat {
	case manifest.FormatBinary:
		installedPath = j.BinaryPath(asset)
		err = j.place(ctx, download, installedPath)
	case manifest.FormatSDK:
		installedPath, err = j.installSDK(asset, download, work)
	default:
		installedPath, err = j.installFromArchive(ctx, asset, download, work)
	}
	if err != nil {
		return err
	}

	j.cleanupHomePaths(action.Tool.CleanupPaths)

	if hook, ok := j.postInstall[action.Name]; ok {
		info := InstallInfo{Name: action.Name, Version: action.To, Asset: asset, Path: installedPath}
		if err := hook(ctx, info); err != nil {
			return fmt.Errorf("post-install hook: %w", err)
		}
	}
	return nil
}

// download fetches the asset to dest and verifies its checksum, streaming
// the verification when the Fetcher supports it.
func (j *Jig) download(ctx context.Context, asset manifest.Asset, dest string) error {
	if vf, ok := j.deps.Fetcher.(VerifyingFetcher); ok {
		if err := vf.DownloadVerified(ctx, asset.URL, dest, asset.SHA256); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		return nil
	}
	if err := j.deps.Fetcher.Download(ctx, asset.URL, dest); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	data, err := j.deps.FS.ReadFile(dest)
	if err != nil {
		return fmt.Errorf("read download: %w", err)
	}
	if err := fetch.VerifySHA256(data, asset.SHA256); err != nil {
		_ = j.deps.FS.Remove(dest)
		return fmt.Errorf("verify download: %w", err)
	}
	return nil
}

func (j *Jig) installFromArchive(ctx context.Context, asset manifest.Asset, archivePath, work string) (string, error) {
	extracted := filepath.Join(work, "extract")
	if _, err := j.deps.Extractor.Extract(archivePath, asset.ArchiveFormat, extracted); err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}

	binary := j.BinaryPath(asset)
	src := filepath.Join(extracted, filepath.FromSlash(cmp.Or(asset.BinaryPath, asset.BinaryName)))
	if err := j.place(ctx, src, binary); err != nil {
		return "", err
	}
	for _, extra := range asset.ExtraBinaries {
		src := filepath.Join(extracted, filepath.FromSlash(extra))
		if err := j.place(ctx, src, filepath.Join(j.dirs.Bin, path.Base(extra))); err != nil {
			return "", err
		}
	}
	return binary, nil
}

// installSDK moves the archive's SDK directory to Base/<sdk_dir>, replacing
// any previous copy only once the new one is ready and restoring it if the
// move fails.
func (j *Jig) installSDK(asset manifest.Asset, archivePath, work string) (string, error) {
	extracted := filepath.Join(work, "extract")
	if _, err := j.deps.Extractor.Extract(archivePath, sdkArchiveFormat(asset.URL), extracted); err != nil {
		return "", fmt.Errorf("extract: %w", err)
	}
	src := filepath.Join(extracted, filepath.FromSlash(asset.SDKDir))
	if !j.deps.FS.Exists(src) {
		return "", fmt.Errorf("archive has no %q directory", asset.SDKDir)
	}

	dest := j.SDKPath(asset)
	if err := j.deps.FS.MkdirAll(filepath.Dir(dest), _dirPerm); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}
	backup := dest + ".jig-old"
	if err := j.removeIfExists(backup); err != nil {
		return "", fmt.Errorf("clear %s: %w", backup, err)
	}
	hadPrevious := j.deps.FS.Exists(dest)
	if hadPrevious {
		if err := j.deps.FS.Rename(dest, backup); err != nil {
			return "", fmt.Errorf("move previous %s aside: %w", dest, err)
		}
	}
	if err := j.deps.FS.Rename(src, dest); err != nil {
		if hadPrevious {
			if restoreErr := j.deps.FS.Rename(backup, dest); restoreErr != nil {
				err = errors.Join(err, fmt.Errorf("restore previous %s: %w", dest, restoreErr))
			}
		}
		return "", fmt.Errorf("move %s into place: %w", asset.SDKDir, err)
	}
	_ = j.deps.FS.Remove(backup)
	return dest, nil
}

// removeIfExists removes path when it exists. Checking first keeps jig
// working with filesystems whose Remove rejects missing paths.
func (j *Jig) removeIfExists(path string) error {
	if !j.deps.FS.Exists(path) {
		return nil
	}
	return j.deps.FS.Remove(path)
}

// place installs the file at src as an executable at dest.
func (j *Jig) place(ctx context.Context, src, dest string) error {
	data, err := j.deps.FS.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	tmp := dest + ".jig-new"
	if err := j.deps.FS.WriteFile(tmp, data, _binaryPerm); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := j.deps.FS.Chmod(tmp, _binaryPerm); err != nil {
		_ = j.deps.FS.Remove(tmp)
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if err := j.deps.FS.Rename(tmp, dest); err != nil {
		_ = j.deps.FS.Remove(tmp)
		return fmt.Errorf("install %s: %w", dest, err)
	}
	if j.platform.OS == "darwin" {
		// Downloads can carry the quarantine attribute, which makes
		// Gatekeeper block the binary. Its absence is not an error.
		_, _ = j.deps.Runner.Run(ctx, "xattr", "-d", "com.apple.quarantine", dest)
	}
	return nil
}

// cleanupHomePaths removes stale home-relative paths. It is best effort:
// unsafe or missing paths are skipped and errors are ignored.
func (j *Jig) cleanupHomePaths(paths []string) {
	if j.dirs.Home == "" {
		return
	}
	for _, p := range paths {
		if check.IsLocalPath(p) {
			_ = j.deps.FS.Remove(filepath.Join(j.dirs.Home, filepath.FromSlash(p)))
		}
	}
}

// downloadName picks the file name for a download: the last element of the
// URL path, which extractors use to name a single decompressed file, or the
// tool name when the URL has no usable one.
func downloadName(tool, rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		if base := path.Base(u.Path); check.IsPlainName(base) {
			return base
		}
	}
	return tool
}

// sdkArchiveFormat is the archive format an SDK asset is packed in, judged by
// its URL.
func sdkArchiveFormat(rawURL string) manifest.Format {
	p := rawURL
	if u, err := url.Parse(rawURL); err == nil {
		p = u.Path
	}
	switch {
	case strings.HasSuffix(p, ".zip"):
		return manifest.FormatZip
	case strings.HasSuffix(p, ".tar.bz2"), strings.HasSuffix(p, ".tbz2"):
		return manifest.FormatTarBz2
	default:
		return manifest.FormatTarGz
	}
}
