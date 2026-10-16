// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rauny-brandao/jig/manifest"
)

// EventType says what happened to an action.
type EventType string

// Event types. Every action emits EventStarted, unless it is blocked or a
// requirement failed, then exactly one of the others.
const (
	// EventStarted is sent when an action begins.
	EventStarted EventType = "started"
	// EventSucceeded is sent when an action completes.
	EventSucceeded EventType = "succeeded"
	// EventSkipped is sent when there was nothing to do, such as removing
	// an item that was already gone.
	EventSkipped EventType = "skipped"
	// EventFailed is sent when an action fails; Event.Err says why.
	EventFailed EventType = "failed"
)

// Event reports progress on one action.
type Event struct {
	Type   EventType
	Action Action
	Err    error
}

// Result is the outcome of one action. Err is nil when it succeeded or was
// skipped.
type Result struct {
	Action  Action
	Skipped bool
	Err     error
}

// Results are the outcomes of applying a plan, in plan order.
type Results []Result

// Err joins the errors of every failed action, or returns nil when none
// failed.
func (rs Results) Err() error {
	var errs []error
	for _, r := range rs {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", r.Action.Op, r.Action.Name, r.Err))
		}
	}
	return errors.Join(errs...)
}

// Apply carries out the plan. Independent actions run in parallel, up to
// the configured parallelism; an action starts only after the actions on
// the items it depends on have finished, and extras of the same kind run one
// at a time because their owning tool may not tolerate concurrent changes.
// An install whose requirement failed is not attempted. A failed action does
// not stop unrelated ones; check Results.Err. onEvent, which may be nil, is
// never called concurrently. With WithRecord, Apply then updates the install
// record. The error is non-nil only when ctx ends before the plan finishes or
// the record could not be updated; the results are complete either way.
func (j *Jig) Apply(ctx context.Context, plan Plan, onEvent func(Event)) (Results, error) {
	var emitMu sync.Mutex
	emit := func(e Event) {
		if onEvent == nil {
			return
		}
		emitMu.Lock()
		defer emitMu.Unlock()
		onEvent(e)
	}

	env := j.Env(plan.source)
	waits := dependencies(plan.Actions)
	done := make([]chan struct{}, len(plan.Actions))
	for i := range done {
		done[i] = make(chan struct{})
	}
	kindLocks := map[Kind]*sync.Mutex{}
	checks := map[Kind]func() error{}
	for _, a := range plan.Actions {
		if a.Kind == KindTool {
			continue
		}
		if _, ok := kindLocks[a.Kind]; !ok {
			kindLocks[a.Kind] = &sync.Mutex{}
		}
		if check, ok := j.beforeKind[a.Kind]; ok && a.Op != OpRemove {
			checks[a.Kind] = sync.OnceValue(func() error { return check(ctx) })
		}
	}

	results := make(Results, len(plan.Actions))
	sem := make(chan struct{}, j.parallelism)
	var wg sync.WaitGroup
	for i, action := range plan.Actions {
		results[i].Action = action
		wg.Go(func() {
			defer close(done[i])
			fail := func(err error) {
				results[i].Err = err
				emit(Event{Type: EventFailed, Action: action, Err: err})
			}

			for _, dep := range waits[i] {
				select {
				case <-done[dep]:
				case <-ctx.Done():
					results[i].Err = ctx.Err()
					return
				}
				if action.Op != OpRemove && results[dep].Err != nil {
					fail(fmt.Errorf("%w: %s", ErrRequirementFailed, plan.Actions[dep].Name))
					return
				}
			}
			if action.Blocked != nil {
				fail(action.Blocked)
				return
			}

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[i].Err = ctx.Err()
				return
			}
			if lock, ok := kindLocks[action.Kind]; ok {
				lock.Lock()
				defer lock.Unlock()
			}
			if check, ok := checks[action.Kind]; ok {
				if err := check(); err != nil {
					fail(err)
					return
				}
			}

			emit(Event{Type: EventStarted, Action: action})
			err := j.apply(ctx, env, action)
			if errors.Is(err, ErrNotInstalled) {
				results[i].Skipped = true
				emit(Event{Type: EventSkipped, Action: action})
				return
			}
			if err != nil {
				fail(err)
				return
			}
			emit(Event{Type: EventSucceeded, Action: action})
		})
	}
	wg.Wait()

	var errs []error
	if err := ctx.Err(); err != nil {
		errs = append(errs, fmt.Errorf("apply: %w", err))
	}
	if err := j.updateRecord(plan, results); err != nil {
		errs = append(errs, fmt.Errorf("apply: update record: %w", err))
	}
	return results, errors.Join(errs...)
}

func (j *Jig) apply(ctx context.Context, env Env, action Action) error {
	if action.Op == OpRemove {
		if action.Kind == KindTool {
			return j.removeTool(action)
		}
		h, err := j.handler(action.Kind)
		if err != nil {
			return err
		}
		return h.Remove(ctx, env, action.Extra)
	}

	if action.Kind == KindTool {
		return j.installTool(ctx, action)
	}
	h, err := j.handler(action.Kind)
	if err != nil {
		return err
	}
	if err := h.Install(ctx, env, action.Extra); err != nil {
		return err
	}
	if hook, ok := j.postInstall[action.Name]; ok {
		if err := hook(ctx, InstallInfo{Name: action.Name, Version: action.To}); err != nil {
			return fmt.Errorf("post-install hook: %w", err)
		}
	}
	return nil
}

func (j *Jig) handler(kind Kind) (Handler, error) {
	h, ok := j.handlers[kind]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrUnknownKind, kind)
	}
	return h, nil
}

// dependencies returns, for each action, the indexes of the actions that must
// finish first: for installs, the actions on the items it requires; for
// removals, the removals of the items that require it.
func dependencies(actions []Action) [][]int {
	index := map[string]int{}
	for i, a := range actions {
		index[a.Name] = i
	}
	waits := make([][]int, len(actions))
	for i, a := range actions {
		for _, req := range a.Extra.Requires {
			dep, ok := index[req]
			if !ok {
				continue
			}
			if a.Op == OpRemove {
				if actions[dep].Op == OpRemove {
					waits[dep] = append(waits[dep], i)
				}
			} else if actions[dep].Op != OpRemove {
				waits[i] = append(waits[i], dep)
			}
		}
	}
	return waits
}

// sourceOrEmpty lets plans built by hand, without a manifest, still apply.
func sourceOrEmpty(m *manifest.Manifest) *manifest.Manifest {
	if m == nil {
		return &manifest.Manifest{}
	}
	return m
}
