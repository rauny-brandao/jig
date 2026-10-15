// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/rauny-brandao/jig/fetch"
	"github.com/rauny-brandao/jig/manifest"
)

// ErrChecksumMismatch means a download did not match the manifest's SHA-256.
var ErrChecksumMismatch = fetch.ErrChecksumMismatch

// FS is the filesystem jig installs into.
type FS interface {
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	// Exists reports whether anything exists at path.
	Exists(path string) bool
	// Remove deletes path and everything below it. A missing path is not
	// an error.
	Remove(path string) error
	// Rename moves a file or directory, replacing an existing file at
	// newpath.
	Rename(oldpath, newpath string) error
	Chmod(name string, mode fs.FileMode) error
}

// Fetcher downloads a URL to a local file.
type Fetcher interface {
	Download(ctx context.Context, url, dest string) error
}

// VerifyingFetcher is a Fetcher that can check the SHA-256 while it
// downloads. jig uses DownloadVerified when available; otherwise it reads
// the downloaded file back to verify it. DownloadVerified must leave nothing
// at dest when the checksum does not match.
type VerifyingFetcher interface {
	Fetcher
	DownloadVerified(ctx context.Context, url, dest, sha256 string) error
}

// Extractor unpacks an archive of the given manifest.Format into destDir and
// returns the paths it created.
type Extractor interface {
	Extract(archivePath, format, destDir string) ([]string, error)
}

// CommandResult is the captured outcome of a command that ran.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner runs external commands. A command that starts and exits non-zero
// returns its result and a nil error; an error means it could not run.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (CommandResult, error)
	// RunWithEnv is Run with extra environment variables ("KEY=value").
	RunWithEnv(ctx context.Context, env []string, name string, args ...string) (CommandResult, error)
}

// Deps are the side-effecting dependencies jig uses. Package host provides
// real implementations and package jigtest in-memory fakes.
type Deps struct {
	FS        FS
	Fetcher   Fetcher
	Extractor Extractor
	Runner    Runner
	// Getenv reads environment variables for handlers; nil means every
	// variable is unset.
	Getenv func(key string) string
}

// Dirs are the directories jig manages.
type Dirs struct {
	// Base holds SDK-format tools, each under Base/<sdk_dir>.
	Base string
	// Bin holds installed binaries.
	Bin string
	// Cache holds downloads while they are installed. It must be on the
	// same filesystem as Base so SDK directories can be moved into place.
	Cache string
	// Home is the directory cleanup_paths are relative to. When empty,
	// cleanup_paths are ignored.
	Home string
}

// Installed describes a tool that was just installed, for post-install hooks.
type Installed struct {
	Name    string
	Version string
	Asset   manifest.Asset
	// Path is the installed binary, or the SDK directory for FormatSDK.
	Path string
}

// PostInstallHook runs after a tool is installed. An error marks the install
// as failed.
type PostInstallHook func(ctx context.Context, installed Installed) error

// Option configures a Jig.
type Option func(*Jig)

// WithParallelism sets how many tools are checked or installed at once.
// Values below 1 are treated as 1. The default is 4.
func WithParallelism(n int) Option {
	return func(j *Jig) { j.parallelism = max(n, 1) }
}

// WithPostInstall registers a hook that runs after the named tool or extra
// installs.
func WithPostInstall(name string, hook PostInstallHook) Option {
	return func(j *Jig) { j.postInstall[name] = hook }
}

// WithHandlers registers handlers for extra kinds, replacing any handler
// already registered for the same kind.
func WithHandlers(handlers ...Handler) Option {
	return func(j *Jig) {
		for _, h := range handlers {
			j.handlers[h.Kind()] = h
		}
	}
}

// WithBeforeKind registers a check that runs once per Apply, before the
// first action on an extra of kind. If it fails, every action of that kind
// fails with its error; use it for preconditions such as being logged in.
func WithBeforeKind(kind Kind, check func(ctx context.Context) error) Option {
	return func(j *Jig) { j.beforeKind[kind] = check }
}

// WithCommandsFromPath makes handlers run the tools that own extras by bare
// name, found on PATH, instead of the managed binaries. Use it for health
// checks that should see what the user's shell runs, which may be another
// copy of the tool.
func WithCommandsFromPath() Option {
	return func(j *Jig) { j.commandsFromPath = true }
}

// WithTimeouts sets how long handlers may run read-only probes and
// installs or removals. The defaults are 2 and 15 minutes.
func WithTimeouts(probe, mutate time.Duration) Option {
	return func(j *Jig) { j.probeTimeout, j.mutateTimeout = probe, mutate }
}

// Jig checks, installs and removes the tools and extras in a manifest.
type Jig struct {
	deps          Deps
	dirs          Dirs
	platform      manifest.Platform
	parallelism   int
	postInstall   map[string]PostInstallHook
	handlers      map[Kind]Handler
	beforeKind    map[Kind]func(context.Context) error
	probeTimeout  time.Duration
	mutateTimeout time.Duration
	// commandsFromPath makes Env.Command return bare tool names.
	commandsFromPath bool
}

// New returns a Jig that installs tools for platform into dirs.
func New(deps Deps, dirs Dirs, platform manifest.Platform, opts ...Option) (*Jig, error) {
	var errs []error
	if deps.FS == nil {
		errs = append(errs, errors.New("deps.FS is required"))
	}
	if deps.Fetcher == nil {
		errs = append(errs, errors.New("deps.Fetcher is required"))
	}
	if deps.Extractor == nil {
		errs = append(errs, errors.New("deps.Extractor is required"))
	}
	if deps.Runner == nil {
		errs = append(errs, errors.New("deps.Runner is required"))
	}
	if dirs.Base == "" || dirs.Bin == "" || dirs.Cache == "" {
		errs = append(errs, errors.New("dirs.Base, dirs.Bin and dirs.Cache are required"))
	}
	if platform.OS == "" || platform.Arch == "" {
		errs = append(errs, errors.New("platform OS and Arch are required"))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("new jig: %w", err)
	}

	if deps.Getenv == nil {
		deps.Getenv = func(string) string { return "" }
	}
	j := &Jig{
		deps:          deps,
		dirs:          dirs,
		platform:      platform,
		parallelism:   4,
		postInstall:   map[string]PostInstallHook{},
		handlers:      map[Kind]Handler{},
		beforeKind:    map[Kind]func(context.Context) error{},
		probeTimeout:  2 * time.Minute,
		mutateTimeout: 15 * time.Minute,
	}
	for _, opt := range opts {
		opt(j)
	}
	return j, nil
}

// BinaryPath returns where the asset's binary is installed: inside the SDK
// directory for FormatSDK, otherwise in the bin directory.
func (j *Jig) BinaryPath(asset manifest.Asset) string {
	if asset.ArchiveFormat == manifest.FormatSDK {
		return filepath.Join(j.SDKPath(asset), "bin", asset.BinaryName)
	}
	return filepath.Join(j.dirs.Bin, asset.BinaryName)
}

// SDKPath returns where a FormatSDK asset's directory is installed.
func (j *Jig) SDKPath(asset manifest.Asset) string {
	return filepath.Join(j.dirs.Base, filepath.FromSlash(asset.SDKDir))
}

// Validate checks the manifest and, for every extra that applies to this
// platform, that a handler is registered for its kind and accepts its fields.
func (j *Jig) Validate(m *manifest.Manifest) error {
	if err := m.Validate(); err != nil {
		return err
	}
	var errs []error
	for _, e := range m.Extras {
		h, ok := j.handlers[Kind(e.Kind)]
		if !ok {
			errs = append(errs, fmt.Errorf("extra %q: %w %q (registered: %v)", e.Name, ErrUnknownKind, e.Kind, j.kinds()))
			continue
		}
		if err := h.Validate(e); err != nil {
			errs = append(errs, fmt.Errorf("extra %q: %w", e.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (j *Jig) kinds() []Kind { return slices.Sorted(maps.Keys(j.handlers)) }

// Env returns the environment handlers get for manifest m, so a handler can
// be called directly, for example from a health check or a test.
func (j *Jig) Env(m *manifest.Manifest) Env {
	m = sourceOrEmpty(m)
	return Env{
		Runner:        j.deps.Runner,
		FS:            j.deps.FS,
		Platform:      j.platform,
		Dirs:          j.dirs,
		Getenv:        j.deps.Getenv,
		ProbeTimeout:  j.probeTimeout,
		MutateTimeout: j.mutateTimeout,
		command: func(tool string) string {
			if j.commandsFromPath {
				return tool
			}
			if asset, err := m.Asset(tool, j.platform.Key()); err == nil {
				return j.BinaryPath(asset)
			}
			return tool
		},
		extra: m.Extra,
	}
}
