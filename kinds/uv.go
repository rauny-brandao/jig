// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package kinds

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

// UVTool installs Python applications with `uv tool install`, placing their
// executables in jig's bin directory. Fields:
//
//	source  what to install: a package name or a git URL (required)
//
// Hooks run the installed executable: post_install after installing and
// pre_uninstall, best effort, before removing. A source that tracks a branch
// should set reinstall: always.
type UVTool struct{}

var _uvToolFields = fields{required: []string{"source"}, hooks: true}

// Kind implements jig.Handler.
func (UVTool) Kind() jig.Kind { return "uv-tool" }

// Validate implements jig.Handler.
func (UVTool) Validate(e manifest.Extra) error { return _uvToolFields.validate(e) }

// Detect implements jig.Handler with one `uv tool list`, whose output lists
// each tool as "<name> v<version>" followed by "- <executable>" lines.
func (UVTool) Detect(ctx context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	found := map[string]jig.Detected{}
	if len(extras) == 0 {
		return found, nil
	}
	result, err := env.Probe(ctx, env.Command("uv"), "tool", "list")
	if err != nil {
		return nil, err
	}
	listed := map[string]string{}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		name, rest, _ := strings.Cut(line, " ")
		ver, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
		listed[name] = strings.TrimPrefix(ver, "v")
	}
	for _, e := range extras {
		ver, ok := listed[e.Name]
		found[e.Name] = jig.Detected{Installed: ok, Version: ver}
	}
	return found, nil
}

// Install implements jig.Handler.
func (UVTool) Install(ctx context.Context, env jig.Env, e manifest.Extra) error {
	vars := []string{"UV_TOOL_BIN_DIR=" + env.Dirs.Bin}
	if _, err := env.Mutate(ctx, vars, env.Command("uv"), "tool", "install", e.Param("source"), "--reinstall"); err != nil {
		return err
	}
	if len(e.Hooks.PostInstall) == 0 {
		return nil
	}
	if _, err := env.Mutate(ctx, vars, toolExecutable(env, e), e.Hooks.PostInstall...); err != nil {
		return fmt.Errorf("post_install hook: %w", err)
	}
	return nil
}

// Remove implements jig.Handler. The pre_uninstall hook only undoes the
// tool's own side effects, so its failure does not stop the removal.
func (h UVTool) Remove(ctx context.Context, env jig.Env, e manifest.Extra) error {
	return removeIfInstalled(ctx, h, env, e, func() error {
		if len(e.Hooks.PreUninstall) > 0 {
			_, _ = env.Mutate(ctx, nil, toolExecutable(env, e), e.Hooks.PreUninstall...)
		}
		_, err := env.Mutate(ctx, nil, env.Command("uv"), "tool", "uninstall", e.Name)
		return err
	})
}

func toolExecutable(env jig.Env, e manifest.Extra) string {
	return filepath.Join(env.Dirs.Bin, executable(env, e.Name))
}
