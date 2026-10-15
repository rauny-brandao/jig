// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package manifest

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// ReinstallAlways makes an installed extra reinstall on every apply, for
// items that track a moving source such as a git branch.
const ReinstallAlways = "always"

// Extra is an item installed through another tool rather than downloaded
// directly, such as a CLI extension, a language runtime or a global package.
// Its Kind selects the handler that installs it; fields the handler needs
// beyond the common ones are kept in Params.
type Extra struct {
	// Kind names the handler, such as "gh-extension" or "npm-global".
	Kind string `yaml:"kind" json:"kind"`
	// Name identifies the item. It must be unique across tools and extras.
	Name string `yaml:"name" json:"name"`
	// Requires lists the tools and extras that must be installed first and
	// removed last.
	Requires []string `yaml:"requires,omitempty" json:"requires,omitempty"`
	// Platforms limits the item to operating systems ("darwin") or
	// platform keys ("darwin_arm64"). Empty means every platform.
	Platforms []string `yaml:"platforms,omitempty" json:"platforms,omitempty"`
	// Reinstall is "always" to reinstall the item on every apply.
	Reinstall string `yaml:"reinstall,omitempty" json:"reinstall,omitempty"`
	// Hooks are arguments run with the item's own executable, for kinds
	// that support them.
	Hooks Hooks `yaml:"hooks,omitempty" json:"hooks,omitzero"`
	// Params holds the kind-specific fields, such as repo or version. In
	// YAML they sit beside the common fields; in JSON under "params".
	Params map[string]string `yaml:",inline" json:"params,omitempty"`
}

// Hooks are arguments passed to an extra's own executable around install
// and removal.
type Hooks struct {
	PostInstall  []string `yaml:"post_install,omitempty" json:"post_install,omitempty"`
	PreUninstall []string `yaml:"pre_uninstall,omitempty" json:"pre_uninstall,omitempty"`
}

// IsZero reports whether no hooks are set.
func (h Hooks) IsZero() bool { return len(h.PostInstall) == 0 && len(h.PreUninstall) == 0 }

// Param returns a kind-specific field, or "" when unset.
func (e Extra) Param(key string) string { return e.Params[key] }

// AppliesTo reports whether the extra is installed on platform.
func (e Extra) AppliesTo(p Platform) bool {
	return len(e.Platforms) == 0 || slices.Contains(e.Platforms, p.OS) || slices.Contains(e.Platforms, p.Key())
}

// Extra returns the extra with the given name.
func (m *Manifest) Extra(name string) (Extra, bool) {
	i := slices.IndexFunc(m.Extras, func(e Extra) bool { return e.Name == name })
	if i < 0 {
		return Extra{}, false
	}
	return m.Extras[i], true
}

// Order returns every tool and extra name so that each item comes after
// everything it requires: tools first in name order, then extras in
// declaration order wherever their requirements allow. It fails when
// requirements form a cycle or name an unknown item.
func (m *Manifest) Order() ([]string, error) {
	known := map[string]bool{}
	for name := range m.Tools {
		known[name] = true
	}
	for _, e := range m.Extras {
		known[e.Name] = true
	}
	for _, e := range m.Extras {
		for _, req := range e.Requires {
			if !known[req] {
				return nil, fmt.Errorf("extra %q requires unknown item %q", e.Name, req)
			}
		}
	}

	order := slices.Sorted(maps.Keys(m.Tools))
	placed := map[string]bool{}
	for _, name := range order {
		placed[name] = true
	}
	pending := slices.Clone(m.Extras)
	for len(pending) > 0 {
		progressed := false
		for i := 0; i < len(pending); i++ {
			e := pending[i]
			if !allPlaced(e.Requires, placed) {
				continue
			}
			order = append(order, e.Name)
			placed[e.Name] = true
			pending = slices.Delete(pending, i, i+1)
			progressed = true
			break
		}
		if !progressed {
			names := make([]string, len(pending))
			for i, e := range pending {
				names[i] = e.Name
			}
			return nil, fmt.Errorf("requirements form a cycle among %s", strings.Join(names, ", "))
		}
	}
	return order, nil
}

func allPlaced(names []string, placed map[string]bool) bool {
	for _, name := range names {
		if !placed[name] {
			return false
		}
	}
	return true
}

func (m *Manifest) validateExtras() []error {
	var errs []error
	seen := map[string]bool{}
	for name := range m.Tools {
		seen[name] = true
	}
	for i, e := range m.Extras {
		label := fmt.Sprintf("extra %q", e.Name)
		if e.Name == "" {
			label = fmt.Sprintf("extras[%d]", i)
			errs = append(errs, fmt.Errorf("%s: name is required", label))
		} else if seen[e.Name] {
			errs = append(errs, fmt.Errorf("%s: name is already used by another tool or extra", label))
		}
		seen[e.Name] = true

		if e.Kind == "" {
			errs = append(errs, fmt.Errorf("%s: kind is required", label))
		}
		if e.Reinstall != "" && e.Reinstall != ReinstallAlways {
			errs = append(errs, fmt.Errorf("%s: reinstall must be empty or %q", label, ReinstallAlways))
		}
		for _, req := range e.Requires {
			if req == e.Name {
				errs = append(errs, fmt.Errorf("%s: cannot require itself", label))
			}
		}
		for _, p := range e.Platforms {
			if strings.Contains(p, "_") {
				if _, err := ParseKey(p); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", label, err))
				}
			} else if p == "" {
				errs = append(errs, fmt.Errorf("%s: empty platform", label))
			}
		}
	}
	if len(errs) == 0 {
		if _, err := m.Order(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
