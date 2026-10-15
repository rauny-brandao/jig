// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package kinds

import (
	"context"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/manifest"
)

// GhExtension installs GitHub CLI extensions. Fields:
//
//	repo  the extension's repository, such as "github/gh-stack" (required)
type GhExtension struct{}

var _ghExtensionFields = fields{required: []string{"repo"}}

// Kind implements jig.Handler.
func (GhExtension) Kind() jig.Kind { return "gh-extension" }

// Validate implements jig.Handler.
func (GhExtension) Validate(e manifest.Extra) error { return _ghExtensionFields.validate(e) }

// Detect implements jig.Handler with one `gh extension list`.
func (GhExtension) Detect(ctx context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	found := map[string]jig.Detected{}
	if len(extras) == 0 {
		return found, nil
	}
	result, err := env.Probe(ctx, env.Command("gh"), "extension", "list")
	if err != nil {
		return nil, err
	}
	for _, e := range extras {
		repo := strings.ToLower(e.Param("repo"))
		installed := false
		for line := range strings.SplitSeq(strings.ToLower(result.Stdout), "\n") {
			if strings.Contains(line, repo) {
				installed = true
				break
			}
		}
		found[e.Name] = jig.Detected{Installed: installed}
	}
	return found, nil
}

// Install implements jig.Handler. gh exits non-zero when the extension is
// already installed, so a failure counts only if the extension is absent
// afterwards.
func (h GhExtension) Install(ctx context.Context, env jig.Env, e manifest.Extra) error {
	_, err := env.Mutate(ctx, nil, env.Command("gh"), "extension", "install", e.Param("repo"))
	if err == nil {
		return nil
	}
	if d, detectErr := detectOne(ctx, h, env, e); detectErr == nil && d.Installed {
		return nil
	}
	return err
}

// Remove implements jig.Handler.
func (h GhExtension) Remove(ctx context.Context, env jig.Env, e manifest.Extra) error {
	return removeIfInstalled(ctx, h, env, e, func() error {
		_, err := env.Mutate(ctx, nil, env.Command("gh"), "extension", "remove", e.Param("repo"))
		return err
	})
}
