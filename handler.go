// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rauny-brandao/jig/manifest"
)

// Errors returned for extras.
var (
	// ErrUnknownKind means no handler is registered for an extra's kind.
	ErrUnknownKind = errors.New("no handler registered for kind")
	// ErrNotInstalled is returned by Handler.Remove when the item is
	// already gone; Apply reports the action as skipped, not failed.
	ErrNotInstalled = errors.New("not installed")
	// ErrRequirementFailed means an action was not run because an item it
	// requires failed to install.
	ErrRequirementFailed = errors.New("requirement failed")
	// ErrRequirementMissing means an action was not run because an item it
	// requires is neither installed nor part of the plan.
	ErrRequirementMissing = errors.New("requirement not installed")
)

// Handler installs, detects and removes one kind of extra through the tool
// that owns it. Register handlers with WithHandlers; package kinds provides
// the built-in ones.
type Handler interface {
	// Kind returns the manifest kind this handler manages.
	Kind() Kind
	// Validate checks the kind-specific fields of an extra.
	Validate(extra manifest.Extra) error
	// Detect reports, keyed by extra name, which of the extras are
	// installed. It must not change the machine. An error means the probe
	// itself could not run.
	Detect(ctx context.Context, env Env, extras []manifest.Extra) (map[string]Detected, error)
	// Install installs or reinstalls one extra.
	Install(ctx context.Context, env Env, extra manifest.Extra) error
	// Remove removes one extra, returning an error wrapping
	// ErrNotInstalled when it is already gone.
	Remove(ctx context.Context, env Env, extra manifest.Extra) error
}

// Detected is what a handler found out about one extra.
type Detected struct {
	Installed bool
	// Version is the installed version, when the owning tool reports one.
	Version string
	// Location is where the extra lives on disk, when known.
	Location string
}

// Env gives handlers what they need to reach the tools that own their extras.
type Env struct {
	Runner   Runner
	FS       FS
	Platform manifest.Platform
	Dirs     Dirs
	// Getenv reads an environment variable; it never returns an error.
	Getenv func(key string) string
	// ProbeTimeout bounds read-only commands; MutateTimeout bounds installs
	// and removals, which may download a lot.
	ProbeTimeout, MutateTimeout time.Duration

	command func(tool string) string
	extra   func(name string) (manifest.Extra, bool)
}

// Command returns how to invoke a tool: the managed binary when the manifest
// declares the tool, otherwise its bare name for a PATH lookup.
func (e Env) Command(tool string) string {
	if e.command == nil {
		return tool
	}
	return e.command(tool)
}

// Extra returns another extra from the manifest, for kinds that depend on
// settings of the items they require.
func (e Env) Extra(name string) (manifest.Extra, bool) {
	if e.extra == nil {
		return manifest.Extra{}, false
	}
	return e.extra(name)
}

// Probe runs a read-only command under ProbeTimeout. A non-zero exit is
// returned as a *CommandError.
func (e Env) Probe(ctx context.Context, name string, args ...string) (CommandResult, error) {
	return e.run(ctx, e.ProbeTimeout, nil, name, args)
}

// Mutate runs a changing command under MutateTimeout with extra environment
// variables ("KEY=value"). A non-zero exit is returned as a *CommandError.
func (e Env) Mutate(ctx context.Context, env []string, name string, args ...string) (CommandResult, error) {
	return e.run(ctx, e.MutateTimeout, env, name, args)
}

func (e Env) run(ctx context.Context, timeout time.Duration, env []string, name string, args []string) (CommandResult, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var (
		result CommandResult
		err    error
	)
	if len(env) > 0 {
		result, err = e.Runner.RunWithEnv(ctx, env, name, args...)
	} else {
		result, err = e.Runner.Run(ctx, name, args...)
	}
	if err != nil {
		return result, fmt.Errorf("run %s: %w", commandLine(name, args), err)
	}
	if result.ExitCode != 0 {
		return result, &CommandError{Command: commandLine(name, args), Result: result}
	}
	return result, nil
}

// CommandError is a command that ran but exited non-zero.
type CommandError struct {
	Command string
	Result  CommandResult
}

func (e *CommandError) Error() string {
	detail := strings.TrimSpace(e.Result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(e.Result.Stdout)
	}
	msg := e.Command + ": exit code " + strconv.Itoa(e.Result.ExitCode)
	if detail != "" {
		msg += ": " + detail
	}
	return msg
}

func commandLine(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), " ")
}
