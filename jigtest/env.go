// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package jigtest provides in-memory fakes of jig's dependencies, so code
// that uses jig can be tested without touching the disk, the network or
// real processes. All fakes are safe for concurrent use.
package jigtest

import "github.com/rauny-brandao/jig"

// Env bundles one of each fake, wired to the same in-memory filesystem.
type Env struct {
	FS        *FS
	Fetcher   *Fetcher
	Extractor *Extractor
	Runner    *Runner
	// Vars are the environment variables jig sees. Set them before use.
	Vars map[string]string
}

// NewEnv returns fakes that share one empty filesystem.
func NewEnv() *Env {
	fsys := NewFS()
	return &Env{
		FS:        fsys,
		Fetcher:   NewFetcher(fsys),
		Extractor: NewExtractor(fsys),
		Runner:    NewRunner(),
		Vars:      map[string]string{},
	}
}

// Deps returns the fakes as jig.Deps.
func (e *Env) Deps() jig.Deps {
	return jig.Deps{
		FS: e.FS, Fetcher: e.Fetcher, Extractor: e.Extractor, Runner: e.Runner,
		Getenv: func(key string) string { return e.Vars[key] },
	}
}
