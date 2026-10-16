// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/rauny-brandao/jig/manifest"
	"github.com/rauny-brandao/jig/version"
)

// Kind is the kind of item jig manages: KindTool, or an extra's kind.
type Kind string

// KindTool is a binary downloaded from the manifest's tools section.
const KindTool Kind = "tool"

// State is how an item on the machine compares to the manifest.
type State string

// Item states.
const (
	// StateOK means the pinned version is installed, or the item is
	// installed and has no pin, or its handler reports no version.
	StateOK State = "ok"
	// StateNewer means a version newer than the pinned one is installed.
	// Plan leaves it alone, like StateOK, unless PlanOptions.Downgrade
	// selects it.
	StateNewer State = "newer"
	// StateMissing means the item is not installed, or its version could
	// not be read.
	StateMissing State = "missing"
	// StateOutdated means an older version is installed, or one that
	// differs from the pin and cannot be compared with it.
	StateOutdated State = "outdated"
	// StateUnsupported means the item does not apply to this platform, or
	// requires an item that does not.
	StateUnsupported State = "unsupported"
)

// Installed reports whether the item is on the machine, at any version.
func (s State) Installed() bool {
	return s == StateOK || s == StateNewer || s == StateOutdated
}

// Status is the state of one manifest item on this machine.
type Status struct {
	Name  string
	Kind  Kind
	State State
	// Want is the pinned version ("" for extras without one); Have is the
	// installed one, or "".
	Want, Have string
	// Location is where an extra lives, when its handler reports it.
	Location string
	// Err is set when the item's state could not be determined; it is then
	// reported as missing.
	Err error

	// Tool and Asset are set for tools; Asset is zero when unsupported.
	Tool  manifest.Tool
	Asset manifest.Asset
	// Extra is set for extras.
	Extra manifest.Extra

	source *manifest.Manifest
	// viaPath is set for extras checked through tools on PATH.
	viaPath bool
}

// Status reports the state of every tool and extra in m: tools by name, then
// extras in dependency order. Extras are only probed once everything they
// require is installed; otherwise they are reported missing.
func (j *Jig) Status(ctx context.Context, m *manifest.Manifest) ([]Status, error) {
	if m == nil {
		return nil, errors.New("status: manifest is nil")
	}
	order, err := m.Order()
	if err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	tools := j.toolStatuses(ctx, m)
	extras, err := j.extraStatuses(ctx, m, tools)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("status: %w", err)
	}

	byName := map[string]Status{}
	for _, s := range slices.Concat(tools, extras) {
		byName[s.Name] = s
	}
	statuses := make([]Status, len(order))
	for i, name := range order {
		statuses[i] = byName[name]
		statuses[i].source = m
		statuses[i].viaPath = j.commandsFromPath && statuses[i].Kind != KindTool
	}
	return statuses, nil
}

func (j *Jig) toolStatuses(ctx context.Context, m *manifest.Manifest) []Status {
	names := slices.Sorted(maps.Keys(m.Tools))
	statuses := make([]Status, len(names))
	sem := make(chan struct{}, j.parallelism)
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			statuses[i] = j.toolStatus(ctx, name, m.Tools[name])
		})
	}
	wg.Wait()
	return statuses
}

func (j *Jig) toolStatus(ctx context.Context, name string, tool manifest.Tool) Status {
	status := Status{Name: name, Kind: KindTool, Want: tool.Version, Tool: tool}
	asset, ok := tool.Asset(j.platform.Key())
	if !ok {
		status.State = StateUnsupported
		return status
	}
	status.Asset = asset
	status.Have = j.installedVersion(ctx, tool, asset)
	status.State = compare(status.Have, status.Want)
	return status
}

// compare turns an installed and a wanted version into a state. With no
// wanted version, any installed version is OK. Versions are compared as
// semantic versions, so "v1.2.3", "1.2.3" and "1.2.3+build" are the same;
// versions that differ as text and are not both semantic versions are
// outdated.
func compare(have, want string) State {
	if have == "" {
		return StateMissing
	}
	if want == "" || have == want {
		return StateOK
	}
	h, hErr := version.Parse(have)
	w, wErr := version.Parse(want)
	if hErr != nil || wErr != nil {
		return StateOutdated
	}
	switch c := h.Compare(w); {
	case c > 0:
		return StateNewer
	case c == 0:
		return StateOK
	default:
		return StateOutdated
	}
}

// installedVersion runs the tool's verify command against the managed
// binary. A missing binary, a failed or non-zero run, or unparseable output
// all mean "not installed".
func (j *Jig) installedVersion(ctx context.Context, tool manifest.Tool, asset manifest.Asset) string {
	if len(tool.VerifyCommand) == 0 {
		return ""
	}
	bin := j.BinaryPath(asset)
	if !j.deps.FS.Exists(bin) {
		return ""
	}
	result, err := j.deps.Runner.Run(ctx, bin, tool.VerifyCommand[1:]...)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return version.FromOutput(result.Stdout+result.Stderr, tool.VerifyPattern)
}

// extraStatuses probes extras level by level, so each level sees the state
// of what it requires, batching each kind into one Detect call per level.
func (j *Jig) extraStatuses(ctx context.Context, m *manifest.Manifest, tools []Status) ([]Status, error) {
	known := map[string]Status{}
	for _, s := range tools {
		known[s.Name] = s
	}
	env := j.Env(m)

	pending := slices.Clone(m.Extras)
	var statuses []Status
	for len(pending) > 0 {
		var level, later []manifest.Extra
		for _, e := range pending {
			if requirementsKnown(e, known) {
				level = append(level, e)
			} else {
				later = append(later, e)
			}
		}
		if len(level) == 0 {
			return nil, errors.New("status: extras have unresolvable requirements")
		}

		probe := map[Kind][]manifest.Extra{}
		for _, e := range level {
			s := Status{Name: e.Name, Kind: Kind(e.Kind), Want: e.Param("version"), Extra: e}
			switch {
			case !e.AppliesTo(j.platform) || anyRequirementIn(e, known, StateUnsupported):
				s.State = StateUnsupported
			case !allRequirementsInstalled(e, known):
				s.State = StateMissing
			default:
				probe[s.Kind] = append(probe[s.Kind], e)
				continue
			}
			known[e.Name] = s
		}
		for _, kind := range slices.Sorted(maps.Keys(probe)) {
			for _, s := range j.detect(ctx, env, kind, probe[kind]) {
				known[s.Name] = s
			}
		}
		for _, e := range level {
			statuses = append(statuses, known[e.Name])
		}
		pending = later
	}
	return statuses, nil
}

func (j *Jig) detect(ctx context.Context, env Env, kind Kind, extras []manifest.Extra) []Status {
	statuses := make([]Status, len(extras))
	handler, ok := j.handlers[kind]
	var (
		found map[string]Detected
		err   error
	)
	if !ok {
		err = fmt.Errorf("%w %q", ErrUnknownKind, kind)
	} else {
		found, err = handler.Detect(ctx, env, extras)
		if err != nil {
			err = fmt.Errorf("detect %s: %w", kind, err)
		}
	}
	for i, e := range extras {
		s := Status{Name: e.Name, Kind: kind, Want: e.Param("version"), Extra: e, Err: err}
		if d := found[e.Name]; err == nil && d.Installed {
			s.Have = d.Version
			s.Location = d.Location
			s.State = StateOK
			if d.Version != "" {
				s.State = compare(d.Version, s.Want)
			}
		} else {
			s.State = StateMissing
		}
		statuses[i] = s
	}
	return statuses
}

func requirementsKnown(e manifest.Extra, known map[string]Status) bool {
	for _, req := range e.Requires {
		if _, ok := known[req]; !ok {
			return false
		}
	}
	return true
}

func allRequirementsInstalled(e manifest.Extra, known map[string]Status) bool {
	for _, req := range e.Requires {
		if !known[req].State.Installed() {
			return false
		}
	}
	return true
}

func anyRequirementIn(e manifest.Extra, known map[string]Status, state State) bool {
	for _, req := range e.Requires {
		if known[req].State == state {
			return true
		}
	}
	return false
}
