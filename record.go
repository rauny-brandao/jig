// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/rauny-brandao/jig/manifest"
)

// ErrNoRecord means there is no install record to read: WithRecord was not
// used, or no Apply has written the record yet.
var ErrNoRecord = errors.New("no install record")

// _recordFormat is the version of the record file's layout.
const _recordFormat = 1

// WithRecord keeps a record of the items jig manages in a JSON file at path,
// so they can be removed later without asking the tools that own them, even
// once the manifest no longer declares them. See PlanRemoveRecorded.
//
// Apply updates the record. Items it installs are added, and items it
// removes, or that turn out to be gone or not managed, are dropped. Items
// the plan's statuses found installed are added too, so a machine set up
// before the record existed is recorded on its next Apply, even of an empty
// plan. Extras checked WithCommandsFromPath are not added, because they were
// found through tools jig may not manage. An unreadable record is replaced
// with what Apply knows, and Apply reports that it was.
func WithRecord(path string) Option {
	return func(j *Jig) { j.recordPath = path }
}

// RecordEntry is one item in the install record.
type RecordEntry struct {
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Version is the version installed or last found, or "" when unknown.
	Version string `json:"version,omitempty"`
	// Asset is set for tools: it says which files removing the tool deletes.
	Asset manifest.Asset `json:"asset,omitzero"`
	// Extra is set for extras: its handler removes it.
	Extra manifest.Extra `json:"extra,omitzero"`
}

type recordFile struct {
	Format int           `json:"format"`
	Items  []RecordEntry `json:"items"`
}

// Record returns the items in the install record, by name. It returns an
// error wrapping ErrNoRecord when WithRecord was not used or no record has
// been written yet.
func (j *Jig) Record() ([]RecordEntry, error) {
	j.recordMu.Lock()
	defer j.recordMu.Unlock()
	return j.readRecord()
}

// PlanRemoveRecorded returns the actions that remove every recorded item
// that keep does not declare, in reverse dependency order. With a nil keep,
// every recorded item is removed. Nothing is checked first: removing an item
// that is already gone is reported as skipped.
//
// Use the current manifest as keep to remove items it no longer declares,
// and nil to uninstall everything without asking the tools that own the
// extras. opts.Select sees each recorded item as a Status with Name, Kind,
// Have, Asset and Extra set and no State. It returns an error wrapping
// ErrNoRecord when there is no record to read.
func (j *Jig) PlanRemoveRecorded(keep *manifest.Manifest, opts RemoveOptions) (Plan, error) {
	entries, err := j.Record()
	if err != nil {
		return Plan{}, fmt.Errorf("plan removal: %w", err)
	}
	if keep != nil {
		entries = slices.DeleteFunc(entries, func(e RecordEntry) bool { return declares(keep, e.Name) })
	}

	plan := Plan{source: j.recordSource(keep, entries)}
	for _, e := range slices.Backward(dependencyOrder(entries)) {
		s := Status{Name: e.Name, Kind: e.Kind, Have: e.Version, Asset: e.Asset, Extra: e.Extra}
		if opts.Select != nil && !opts.Select(s) {
			continue
		}
		plan.Actions = append(plan.Actions, actionFor(OpRemove, s))
	}
	return plan, nil
}

// updateRecord brings the record in line with what applying plan found and
// did.
func (j *Jig) updateRecord(plan Plan, results Results) error {
	if j.recordPath == "" {
		return nil
	}
	j.recordMu.Lock()
	defer j.recordMu.Unlock()

	entries, err := j.readRecord()
	var readErr error
	if err != nil && !errors.Is(err, ErrNoRecord) {
		// Start over rather than fail every Apply: the items the statuses
		// found installed are recorded again below.
		entries, readErr = nil, fmt.Errorf("replaced unreadable record: %w", err)
	}
	byName := make(map[string]RecordEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}
	for _, s := range plan.seen {
		if s.State.Installed() && !s.viaPath {
			byName[s.Name] = RecordEntry{Name: s.Name, Kind: s.Kind, Version: s.Have, Asset: s.Asset, Extra: s.Extra}
		}
	}
	for _, r := range results {
		a := r.Action
		switch {
		case a.Op == OpRemove && (r.Err == nil || errors.Is(r.Err, ErrNotManaged)):
			delete(byName, a.Name)
		case a.Op != OpRemove && r.Err == nil && !r.Skipped:
			byName[a.Name] = RecordEntry{Name: a.Name, Kind: a.Kind, Version: a.To, Asset: a.Asset, Extra: a.Extra}
		}
	}
	return errors.Join(readErr, j.writeRecord(slices.SortedFunc(maps.Values(byName), byEntryName)))
}

// readRecord reads the record. The caller holds recordMu.
func (j *Jig) readRecord() ([]RecordEntry, error) {
	if j.recordPath == "" {
		return nil, fmt.Errorf("%w: WithRecord was not used", ErrNoRecord)
	}
	if !j.deps.FS.Exists(j.recordPath) {
		return nil, fmt.Errorf("%w at %s", ErrNoRecord, j.recordPath)
	}
	data, err := j.deps.FS.ReadFile(j.recordPath)
	if err != nil {
		return nil, fmt.Errorf("read record: %w", err)
	}
	var file recordFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("read record %s: %w", j.recordPath, err)
	}
	if file.Format != _recordFormat {
		return nil, fmt.Errorf("read record %s: unsupported format %d", j.recordPath, file.Format)
	}
	return slices.SortedFunc(slices.Values(file.Items), byEntryName), nil
}

// writeRecord replaces the record with entries, unless it already holds
// exactly them. The caller holds recordMu.
func (j *Jig) writeRecord(entries []RecordEntry) error {
	data, err := json.MarshalIndent(recordFile{Format: _recordFormat, Items: entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode record: %w", err)
	}
	data = append(data, '\n')
	if current, err := j.deps.FS.ReadFile(j.recordPath); err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err := j.deps.FS.MkdirAll(filepath.Dir(j.recordPath), _dirPerm); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(j.recordPath), err)
	}
	tmp := j.recordPath + ".jig-new"
	if err := j.deps.FS.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	if err := j.deps.FS.Rename(tmp, j.recordPath); err != nil {
		_ = j.deps.FS.Remove(tmp)
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

// recordSource returns the manifest removals of recorded items run against:
// keep, plus the recorded items it does not declare, so handlers can still
// reach the tools and extras those items need.
func (j *Jig) recordSource(keep *manifest.Manifest, entries []RecordEntry) *manifest.Manifest {
	source := &manifest.Manifest{Tools: map[string]manifest.Tool{}}
	if keep != nil {
		maps.Copy(source.Tools, keep.Tools)
		source.Extras = slices.Clone(keep.Extras)
	}
	for _, e := range entries {
		if e.Kind == KindTool {
			source.Tools[e.Name] = manifest.Tool{Version: e.Version, Platforms: map[string]manifest.Asset{j.platform.Key(): e.Asset}}
		} else {
			source.Extras = append(source.Extras, e.Extra)
		}
	}
	return source
}

// dependencyOrder sorts entries so each comes after the recorded items it
// requires: tools first, then extras, each in name order where requirements
// allow. Requirements that are not recorded are ignored.
func dependencyOrder(entries []RecordEntry) []RecordEntry {
	recorded := map[string]bool{}
	for _, e := range entries {
		recorded[e.Name] = true
	}
	pending := slices.SortedFunc(slices.Values(entries), func(a, b RecordEntry) int {
		if (a.Kind == KindTool) != (b.Kind == KindTool) {
			if a.Kind == KindTool {
				return -1
			}
			return 1
		}
		return byEntryName(a, b)
	})
	placed := map[string]bool{}
	ordered := make([]RecordEntry, 0, len(entries))
	for len(pending) > 0 {
		i := slices.IndexFunc(pending, func(e RecordEntry) bool {
			for _, req := range e.Extra.Requires {
				if recorded[req] && !placed[req] {
					return false
				}
			}
			return true
		})
		// A cycle cannot come from a valid manifest; place the rest as is.
		if i < 0 {
			return append(ordered, pending...)
		}
		ordered = append(ordered, pending[i])
		placed[pending[i].Name] = true
		pending = slices.Delete(pending, i, i+1)
	}
	return ordered
}

func byEntryName(a, b RecordEntry) int { return cmp.Compare(a.Name, b.Name) }

// declares reports whether m declares a tool or extra called name.
func declares(m *manifest.Manifest, name string) bool {
	if _, ok := m.Tools[name]; ok {
		return true
	}
	_, ok := m.Extra(name)
	return ok
}
