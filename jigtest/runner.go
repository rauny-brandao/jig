// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jigtest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/rauny-brandao/jig"
)

// ErrCommandNotFound is returned for commands that have no stub.
var ErrCommandNotFound = errors.New("jigtest: command not found")

// Runner returns canned results for exact command lines and records every
// call. Commands without a stub fail with ErrCommandNotFound, as if they
// were not installed.
type Runner struct {
	mu    sync.Mutex
	stubs map[string]stub
	calls []Call
}

// Call is one recorded command line and the extra environment it ran with.
type Call struct {
	Args []string
	Env  []string
}

type stub struct {
	result jig.CommandResult
	err    error
}

var _ jig.Runner = (*Runner)(nil)

// NewRunner returns a Runner with no stubs.
func NewRunner() *Runner { return &Runner{stubs: map[string]stub{}} }

// Stub makes the command line name+args return result.
func (r *Runner) Stub(result jig.CommandResult, name string, args ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs[commandKey(name, args)] = stub{result: result}
}

// StubError makes the command line name+args fail to run with err.
func (r *Runner) StubError(err error, name string, args ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stubs[commandKey(name, args)] = stub{err: err}
}

// Calls returns every command line run so far, in order.
func (r *Runner) Calls() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([][]string, len(r.calls))
	for i, call := range r.calls {
		calls[i] = slices.Clone(call.Args)
	}
	return calls
}

// CallsWithEnv returns every call so far with its extra environment.
func (r *Runner) CallsWithEnv() []Call {
	r.mu.Lock()
	defer r.mu.Unlock()
	calls := make([]Call, len(r.calls))
	for i, call := range r.calls {
		calls[i] = Call{Args: slices.Clone(call.Args), Env: slices.Clone(call.Env)}
	}
	return calls
}

// Run implements jig.Runner.
func (r *Runner) Run(ctx context.Context, name string, args ...string) (jig.CommandResult, error) {
	return r.RunWithEnv(ctx, nil, name, args...)
}

// RunWithEnv implements jig.Runner. The environment does not affect which
// stub answers; check it with CallsWithEnv.
func (r *Runner) RunWithEnv(ctx context.Context, env []string, name string, args ...string) (jig.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return jig.CommandResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, Call{Args: append([]string{name}, args...), Env: slices.Clone(env)})
	s, ok := r.stubs[commandKey(name, args)]
	if !ok {
		return jig.CommandResult{}, fmt.Errorf("run %s: %w", name, ErrCommandNotFound)
	}
	return s.result, s.err
}

func commandKey(name string, args []string) string {
	return strings.Join(append([]string{name}, args...), "\x00")
}
