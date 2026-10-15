// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package kinds provides jig's built-in extra handlers:
//
//	gh-extension  GitHub CLI extensions          (via gh)
//	uv-tool       Python apps from uv tool       (via uv)
//	node-runtime  a Node.js release channel      (via fnm)
//	npm-global    global npm packages            (via fnm and npm)
//	jdk           a Java Development Kit          (via Coursier, cs)
//
// Detection never changes the machine, and removing an item that is already
// gone reports jig.ErrNotInstalled.
package kinds

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

// Defaults returns every built-in handler.
func Defaults() []jig.Handler {
	return []jig.Handler{GhExtension{}, UVTool{}, NodeRuntime{}, NPMGlobal{}, JDK{}}
}

// fields describes the kind-specific fields a handler accepts.
type fields struct {
	required []string
	optional []string
	hooks    bool
}

func (f fields) validate(e manifest.Extra) error {
	var errs []error
	for _, key := range f.required {
		if e.Param(key) == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(e.Params)) {
		if !slices.Contains(f.required, key) && !slices.Contains(f.optional, key) {
			errs = append(errs, fmt.Errorf("unknown field %q for kind %s (allowed: %s)", key, e.Kind, strings.Join(slices.Concat(f.required, f.optional), ", ")))
		}
	}
	if !f.hooks && !e.Hooks.IsZero() {
		errs = append(errs, fmt.Errorf("kind %s does not support hooks", e.Kind))
	}
	return errors.Join(errs...)
}

// detectOne runs a handler's Detect for a single extra.
func detectOne(ctx context.Context, h jig.Handler, env jig.Env, e manifest.Extra) (jig.Detected, error) {
	found, err := h.Detect(ctx, env, []manifest.Extra{e})
	if err != nil {
		return jig.Detected{}, err
	}
	return found[e.Name], nil
}

// removeIfInstalled checks the extra is installed before running remove, so
// an item that is already gone is reported as jig.ErrNotInstalled rather
// than as whatever the owning tool says about it.
func removeIfInstalled(ctx context.Context, h jig.Handler, env jig.Env, e manifest.Extra, remove func() error) error {
	d, err := detectOne(ctx, h, env, e)
	if err != nil {
		return fmt.Errorf("check %s before removing: %w", e.Name, err)
	}
	if !d.Installed {
		return fmt.Errorf("%s: %w", e.Name, jig.ErrNotInstalled)
	}
	return remove()
}

// executable returns the file name of a program on the env's platform.
func executable(env jig.Env, name string) string {
	if env.Platform.OS == "windows" && !strings.HasSuffix(strings.ToLower(name), ".exe") {
		return name + ".exe"
	}
	return name
}

func firstLine(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
