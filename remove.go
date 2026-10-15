// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"

	"github.com/rauny-brandao/jig/manifest"
)

// removeTool deletes the files jig installs for a tool: its binary and extra
// binaries, or its SDK directory. It returns ErrNotInstalled when none of
// them exist.
func (j *Jig) removeTool(action Action) error {
	var paths []string
	if action.Asset.ArchiveFormat == manifest.FormatSDK {
		paths = append(paths, j.SDKPath(action.Asset))
	} else {
		paths = append(paths, j.BinaryPath(action.Asset))
		for _, extra := range action.Asset.ExtraBinaries {
			paths = append(paths, filepath.Join(j.dirs.Bin, path.Base(extra)))
		}
	}

	var (
		removed int
		errs    []error
	)
	for _, p := range paths {
		if !j.deps.FS.Exists(p) {
			continue
		}
		if err := j.deps.FS.Remove(p); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", p, err))
			continue
		}
		removed++
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if removed == 0 {
		return ErrNotInstalled
	}
	return nil
}
