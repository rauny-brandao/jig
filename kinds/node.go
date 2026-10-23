// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package kinds

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/internal/check"
	"github.com/rauny-brandao/jig/manifest"
	"github.com/rauny-brandao/jig/version"
)

// NodeRuntime installs a Node.js release channel with fnm. Fields:
//
//	channel  the fnm version or alias, such as "lts-latest" or "22" (required)
//	default  "true" to also make it fnm's default, best effort (optional)
type NodeRuntime struct{}

var _nodeRuntimeFields = fields{required: []string{"channel"}, optional: []string{"default"}}

// Kind implements jig.Handler.
func (NodeRuntime) Kind() jig.Kind { return "node-runtime" }

// Validate implements jig.Handler.
func (NodeRuntime) Validate(e manifest.Extra) error {
	if err := _nodeRuntimeFields.validate(e); err != nil {
		return err
	}
	if d := e.Param("default"); d != "" && d != "true" && d != "false" {
		return fmt.Errorf("default must be true or false, got %q", d)
	}
	return nil
}

// Detect implements jig.Handler. The channel must be known to fnm and its
// node must run.
func (NodeRuntime) Detect(ctx context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	found := map[string]jig.Detected{}
	if len(extras) == 0 {
		return found, nil
	}
	fnm := env.Command("fnm")
	listed, err := env.Probe(ctx, fnm, "list")
	if err != nil {
		return nil, err
	}
	for _, e := range extras {
		channel := e.Param("channel")
		if !strings.Contains(listed.Stdout, channel) {
			found[e.Name] = jig.Detected{}
			continue
		}
		result, err := env.Probe(ctx, fnm, "exec", "--using="+channel, "--", "node", "--version")
		if err != nil {
			found[e.Name] = jig.Detected{}
			continue
		}
		found[e.Name] = jig.Detected{Installed: true, Version: strings.TrimPrefix(firstLine(result.Stdout), "v")}
	}
	return found, nil
}

// Install implements jig.Handler.
func (NodeRuntime) Install(ctx context.Context, env jig.Env, e manifest.Extra) error {
	fnm := env.Command("fnm")
	if _, err := env.Mutate(ctx, nil, fnm, "install", e.Param("channel")); err != nil {
		return err
	}
	if e.Param("default") == "true" {
		// The runtime works without being the default, so a failure here
		// must not fail the install.
		_, _ = env.Mutate(ctx, nil, fnm, "default", e.Param("channel"))
	}
	return nil
}

// Remove implements jig.Handler.
func (h NodeRuntime) Remove(ctx context.Context, env jig.Env, e manifest.Extra) error {
	return removeIfInstalled(ctx, h, env, e, func() error {
		_, err := env.Mutate(ctx, nil, env.Command("fnm"), "uninstall", e.Param("channel"))
		return err
	})
}

// NPMGlobal installs a global npm package into a Node.js runtime managed by
// fnm, with install scripts disabled (--ignore-scripts). The extra must
// require a node-runtime extra, whose channel it uses. Fields:
//
//	version    the exact version to install; when unset, the latest is
//	           installed and any installed version counts as current (optional)
//	integrity  the expected npm SHA-512 Subresource Integrity digest
//	           ("sha512-<base64>") of the package tarball; when set, the
//	           tarball is downloaded with `npm pack` and verified against
//	           it before installing (optional, requires version)
type NPMGlobal struct{}

const _sha512Prefix = "sha512-"

var _npmGlobalFields = fields{optional: []string{"version", "integrity"}}

// Kind implements jig.Handler.
func (NPMGlobal) Kind() jig.Kind { return "npm-global" }

// Validate implements jig.Handler.
func (NPMGlobal) Validate(e manifest.Extra) error {
	if err := _npmGlobalFields.validate(e); err != nil {
		return err
	}
	if len(e.Requires) == 0 {
		return errors.New("must require the node-runtime extra it installs into")
	}
	if integrity := e.Param("integrity"); integrity != "" {
		if !check.IsPlainName(npmTarballSlug(e.Name)) {
			return fmt.Errorf("invalid package name %q", e.Name)
		}
		ver := e.Param("version")
		if ver == "" {
			return errors.New("integrity requires version")
		}
		if strings.HasPrefix(ver, "v") {
			return fmt.Errorf("version %q must not have a leading \"v\" when integrity is set", ver)
		}
		if _, err := version.Parse(ver); err != nil {
			return fmt.Errorf("integrity requires an exact semantic version: %w", err)
		}
		if err := validateSHA512Integrity(integrity); err != nil {
			return err
		}
	}
	return nil
}

func validateSHA512Integrity(s string) error {
	encoded, ok := strings.CutPrefix(s, _sha512Prefix)
	if !ok {
		return fmt.Errorf("integrity %q must start with %q", s, _sha512Prefix)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != sha512.Size {
		return fmt.Errorf("integrity %q is not a valid base64-encoded SHA-512 digest", s)
	}
	return nil
}

// Detect implements jig.Handler with one `npm list -g` per package. npm exits
// non-zero for a missing package, which counts as not installed.
func (NPMGlobal) Detect(ctx context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	found := map[string]jig.Detected{}
	for _, e := range extras {
		channel, err := nodeChannel(env, e)
		if err != nil {
			return nil, err
		}
		result, err := env.Probe(ctx, env.Command("fnm"), npmArgs(env, channel, "list", "-g", e.Name, "--json")...)
		if err != nil {
			found[e.Name] = jig.Detected{}
			continue
		}
		ver := npmListedVersion(result.Stdout, e.Name)
		found[e.Name] = jig.Detected{Installed: ver != "", Version: ver}
	}
	return found, nil
}

// Install implements jig.Handler.
func (NPMGlobal) Install(ctx context.Context, env jig.Env, e manifest.Extra) error {
	channel, err := nodeChannel(env, e)
	if err != nil {
		return err
	}
	if integrity := e.Param("integrity"); integrity != "" {
		return installVerifiedNPM(ctx, env, channel, e, integrity)
	}
	pkg := e.Name
	if v := e.Param("version"); v != "" {
		pkg += "@" + v
	}
	_, err = env.Mutate(ctx, nil, env.Command("fnm"), npmArgs(env, channel, "install", "-g", "--ignore-scripts", pkg)...)
	return err
}

func installVerifiedNPM(ctx context.Context, env jig.Env, channel string, e manifest.Extra, wantIntegrity string) error {
	slug := npmTarballSlug(e.Name)
	ver := e.Param("version")
	pkg := e.Name + "@" + ver

	workDir := filepath.Join(env.Dirs.Cache, "npm-"+slug)
	if err := env.FS.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", workDir, err)
	}
	defer func() { _ = env.FS.Remove(workDir) }()

	fnm := env.Command("fnm")
	if _, err := env.Mutate(ctx, nil, fnm, npmArgs(env, channel, "pack", "--location=global", "--ignore-scripts", "--pack-destination", workDir, pkg)...); err != nil {
		return err
	}

	tarball := filepath.Join(workDir, slug+"-"+ver+".tgz")
	data, err := env.FS.ReadFile(tarball)
	if err != nil {
		return fmt.Errorf("read packed tarball %s: %w", tarball, err)
	}
	sum := sha512.Sum512(data)
	if got := _sha512Prefix + base64.StdEncoding.EncodeToString(sum[:]); got != wantIntegrity {
		return fmt.Errorf("verify %s: %w: expected %s, got %s", pkg, jig.ErrChecksumMismatch, wantIntegrity, got)
	}

	_, err = env.Mutate(ctx, nil, fnm, npmArgs(env, channel, "install", "-g", "--ignore-scripts", tarball)...)
	return err
}

// npmTarballSlug converts a package name such as "@asyncapi/cli" into the
// filename stem `npm pack` uses ("asyncapi-cli").
func npmTarballSlug(name string) string {
	return strings.ReplaceAll(strings.TrimPrefix(name, "@"), "/", "-")
}

// Remove implements jig.Handler.
func (h NPMGlobal) Remove(ctx context.Context, env jig.Env, e manifest.Extra) error {
	return removeIfInstalled(ctx, h, env, e, func() error {
		channel, err := nodeChannel(env, e)
		if err != nil {
			return err
		}
		_, err = env.Mutate(ctx, nil, env.Command("fnm"), npmArgs(env, channel, "uninstall", "-g", e.Name)...)
		return err
	})
}

// nodeChannel finds the channel of the node-runtime extra that e requires.
func nodeChannel(env jig.Env, e manifest.Extra) (string, error) {
	for _, req := range e.Requires {
		if runtime, ok := env.Extra(req); ok && runtime.Kind == string(NodeRuntime{}.Kind()) {
			return runtime.Param("channel"), nil
		}
	}
	return "", fmt.Errorf("npm-global %q must require a node-runtime extra", e.Name)
}

// npmArgs builds fnm arguments that run npm inside the channel's runtime.
// npm is a batch script on Windows.
func npmArgs(env jig.Env, channel string, args ...string) []string {
	npm := "npm"
	if env.Platform.OS == "windows" {
		npm = "npm.cmd"
	}
	return append([]string{"exec", "--using=" + channel, "--", npm}, args...)
}

// npmListedVersion reads a package's version from `npm list -g <pkg> --json`.
// npm may print warnings around the JSON, so only the outermost object is
// decoded, and only the named package's entry is read.
func npmListedVersion(output, pkg string) string {
	start, end := strings.Index(output, "{"), strings.LastIndex(output, "}")
	if start < 0 || end < start {
		return ""
	}
	var listed struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(output[start:end+1]), &listed); err != nil {
		return ""
	}
	return listed.Dependencies[pkg].Version
}
