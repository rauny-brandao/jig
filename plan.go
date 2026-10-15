// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"fmt"
	"slices"

	"github.com/rauny-brandao/jig/manifest"
)

// Op is the change an Action makes.
type Op string

// Plan operations.
const (
	// OpInstall installs a missing item.
	OpInstall Op = "install"
	// OpUpgrade replaces an outdated item.
	OpUpgrade Op = "upgrade"
	// OpDowngrade replaces an item newer than its pin with the pinned
	// version: one selected by PlanOptions.Downgrade, or any newer item
	// when the plan is forced or the extra is marked reinstall: always.
	OpDowngrade Op = "downgrade"
	// OpReinstall installs the pinned version over the same version,
	// because the plan was forced or the extra is marked reinstall: always.
	OpReinstall Op = "reinstall"
	// OpRemove removes an installed item.
	OpRemove Op = "remove"
)

// Action is one planned change.
type Action struct {
	Op   Op
	Name string
	Kind Kind
	// From is the installed version ("" when missing); To is the pinned one.
	From, To string
	Tool     manifest.Tool
	Asset    manifest.Asset
	Extra    manifest.Extra
	// Blocked is set when the action cannot run, for example because an
	// item it requires is neither installed nor planned. Apply fails a
	// blocked action without running it.
	Blocked error
}

// Plan is the ordered list of changes Apply will make.
type Plan struct {
	Actions []Action

	source *manifest.Manifest
}

// Empty reports whether the plan changes nothing.
func (p Plan) Empty() bool { return len(p.Actions) == 0 }

// Filter picks statuses, for example the items a plan covers. Named and
// OfKind build common filters; any func(Status) bool works too.
type Filter func(Status) bool

// Named returns a Filter that matches items with one of the given names.
// With no names it matches nothing.
func Named(names ...string) Filter {
	names = slices.Clone(names)
	return func(s Status) bool { return slices.Contains(names, s.Name) }
}

// OfKind returns a Filter that matches items of one of the given kinds.
// With no kinds it matches nothing.
func OfKind(kinds ...Kind) Filter {
	kinds = slices.Clone(kinds)
	return func(s Status) bool { return slices.Contains(kinds, s.Kind) }
}

// PlanOptions narrows or widens an install plan.
type PlanOptions struct {
	// Select limits the plan to the items it matches. Nil means every
	// item. Requirements are still checked against every item, so a
	// selected item whose requirement is neither installed nor selected is
	// blocked.
	Select Filter
	// Force reinstalls items that are already installed and not outdated,
	// at the pinned version. Newer items are planned as downgrades.
	Force bool
	// Downgrade picks which items newer than their pin are replaced with
	// the pinned version. Nil means none: newer items are left alone.
	Downgrade Filter
}

// Plan turns statuses into the actions that bring the machine in line with
// the manifest, in dependency order. Unsupported items are never planned.
// Planning has no side effects, so a plan can be shown as a dry run.
func (j *Jig) Plan(statuses []Status, opts PlanOptions) Plan {
	plan := Plan{source: sourceOf(statuses)}
	for _, s := range statuses {
		if opts.Select != nil && !opts.Select(s) {
			continue
		}
		var op Op
		switch s.State {
		case StateMissing:
			op = OpInstall
		case StateOutdated:
			op = OpUpgrade
		case StateOK, StateNewer:
			forced := opts.Force || s.Extra.Reinstall == manifest.ReinstallAlways
			switch {
			case s.State == StateNewer && (forced || opts.Downgrade != nil && opts.Downgrade(s)):
				op = OpDowngrade
			case forced:
				op = OpReinstall
			default:
				continue
			}
		case StateUnsupported:
			continue
		}
		plan.Actions = append(plan.Actions, actionFor(op, s))
	}
	blockUnmetRequirements(&plan, statuses)
	return plan
}

// RemoveOptions narrows a removal plan.
type RemoveOptions struct {
	// Select limits the plan to the items it matches. Nil means every
	// item.
	Select Filter
	// ExtrasOnly leaves tools in place and removes only extras.
	ExtrasOnly bool
}

// PlanRemove returns the actions that remove every installed item, in
// reverse dependency order so nothing is removed before the items that need
// it. Removing a tool deletes the files jig installed for it; removing an
// extra goes through its handler.
func (j *Jig) PlanRemove(statuses []Status, opts RemoveOptions) Plan {
	plan := Plan{source: sourceOf(statuses)}
	for _, s := range slices.Backward(statuses) {
		if !s.State.Installed() || opts.Select != nil && !opts.Select(s) {
			continue
		}
		if opts.ExtrasOnly && s.Kind == KindTool {
			continue
		}
		plan.Actions = append(plan.Actions, actionFor(OpRemove, s))
	}
	return plan
}

func actionFor(op Op, s Status) Action {
	return Action{
		Op: op, Name: s.Name, Kind: s.Kind, From: s.Have, To: s.Want,
		Tool: s.Tool, Asset: s.Asset, Extra: s.Extra,
	}
}

// blockUnmetRequirements marks install actions whose requirements will be
// neither installed nor installed earlier in the plan.
func blockUnmetRequirements(plan *Plan, statuses []Status) {
	present := map[string]bool{}
	for _, s := range statuses {
		if s.State.Installed() {
			present[s.Name] = true
		}
	}
	for _, a := range plan.Actions {
		present[a.Name] = true
	}
	for i, a := range plan.Actions {
		for _, req := range a.Extra.Requires {
			if !present[req] {
				plan.Actions[i].Blocked = fmt.Errorf("%w: %s", ErrRequirementMissing, req)
				break
			}
		}
	}
}

// sourceOf returns the manifest the statuses were computed from, so Apply
// can resolve the tools and extras that actions refer to.
func sourceOf(statuses []Status) *manifest.Manifest {
	for _, s := range statuses {
		if s.source != nil {
			return s.source
		}
	}
	return nil
}
