// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/jigtest"
	"github.com/rauny-brandao/jig/manifest"
)

var _recordPath = filepath.Join(_dirs.Base, "record.json")

func recordedNames(t *testing.T, j *jig.Jig) []string {
	t.Helper()
	entries, err := j.Record()
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

func actionNames(plan jig.Plan) []string {
	var names []string
	for _, a := range plan.Actions {
		names = append(names, a.Name)
	}
	return names
}

func applyPlan(t *testing.T, j *jig.Jig, plan jig.Plan) jig.Results {
	t.Helper()
	results, err := j.Apply(t.Context(), plan, nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if err := results.Err(); err != nil {
		t.Fatalf("Apply() results error = %v", err)
	}
	return results
}

func TestRecordIsMissingUntilWritten(t *testing.T) {
	env := jigtest.NewEnv()
	for name, j := range map[string]*jig.Jig{
		"without WithRecord": newJig(t, env, _linux),
		"not written yet":    newJig(t, env, _linux, jig.WithRecord(_recordPath)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := j.Record(); !errors.Is(err, jig.ErrNoRecord) {
				t.Errorf("Record() error = %v, want ErrNoRecord", err)
			}
			if _, err := j.PlanRemoveRecorded(nil, jig.RemoveOptions{}); !errors.Is(err, jig.ErrNoRecord) {
				t.Errorf("PlanRemoveRecorded() error = %v, want ErrNoRecord", err)
			}
		})
	}
}

func TestApplyRecordsWhatItInstalls(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	m := extrasManifest(env)

	if results, _ := applyAll(t, j, m); results.Err() != nil {
		t.Fatal(results.Err())
	}

	entries, err := j.Record()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]jig.RecordEntry{}
	for _, e := range entries {
		byName[e.Name] = e
	}
	if got := recordedNames(t, j); !slices.Equal(got, []string{"fnm", "gh", "node", "stack", "typescript"}) {
		t.Fatalf("recorded %v", got)
	}
	if gh := byName["gh"]; gh.Kind != jig.KindTool || gh.Version != "2.0.0" || gh.Asset.BinaryName != "gh" || gh.Asset.URL != "https://example.com/gh/v2.0.0/gh_2.0.0.tar.gz" {
		t.Errorf("gh entry = %+v, want the resolved asset at 2.0.0", gh)
	}
	if ts := byName["typescript"]; ts.Kind != "npm" || ts.Version != "5.0.0" || !slices.Equal(ts.Extra.Requires, []string{"node"}) || ts.Extra.Param("version") != "5.0.0" {
		t.Errorf("typescript entry = %+v, want the extra at 5.0.0", ts)
	}
}

func TestApplyAdoptsItemsFoundInstalled(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	m := extrasManifest(env)
	stubInstalled(env, filepath.Join(_dirs.Bin, "fnm"), "fnm", "1.0.0")
	f.runtime.installed["node"] = ""

	for _, tt := range []struct {
		name string
		opts []jig.Option
		want []string
	}{
		{"managed tools", nil, []string{"fnm", "node"}},
		{"tools from PATH", []jig.Option{jig.WithCommandsFromPath()}, []string{"fnm"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record.json")
			j := newJig(t, env, _linux, append(tt.opts, f.option(), jig.WithRecord(path))...)
			statuses, err := j.Status(t.Context(), m)
			if err != nil {
				t.Fatal(err)
			}
			plan := j.Plan(statuses, jig.PlanOptions{Select: jig.Named()})
			if !plan.Empty() {
				t.Fatalf("plan = %v, want empty", actionNames(plan))
			}
			applyPlan(t, j, plan)
			if got := recordedNames(t, j); !slices.Equal(got, tt.want) {
				t.Errorf("recorded %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyDropsRemovedItemsFromTheRecord(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	m := extrasManifest(env)
	if results, _ := applyAll(t, j, m); results.Err() != nil {
		t.Fatal(results.Err())
	}

	stubInstalled(env, filepath.Join(_dirs.Bin, "gh"), "gh", "2.0.0")
	stubInstalled(env, filepath.Join(_dirs.Bin, "fnm"), "fnm", "1.0.0")
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	// stack is gone by the time it is removed: the removal is skipped and
	// it leaves the record all the same.
	delete(f.ext.installed, "stack")
	results := applyPlan(t, j, j.PlanRemove(statuses, jig.RemoveOptions{Select: jig.Named("typescript", "stack")}))
	if !slices.ContainsFunc(results, func(r jig.Result) bool { return r.Action.Name == "stack" && r.Skipped }) {
		t.Errorf("stack removal was not skipped: %+v", results)
	}
	if got := recordedNames(t, j); !slices.Equal(got, []string{"fnm", "gh", "node"}) {
		t.Errorf("recorded %v, want fnm, gh and node", got)
	}
}

func TestApplyKeepsTheRecordOfFailedActions(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	m := extrasManifest(env)
	f.npm.installErr["typescript"] = errors.New("registry down")

	results, _ := applyAll(t, j, m)
	if results.Err() == nil {
		t.Fatal("Apply() succeeded, want typescript to fail")
	}
	if got := recordedNames(t, j); !slices.Equal(got, []string{"fnm", "gh", "node", "stack"}) {
		t.Errorf("recorded %v, want everything but typescript", got)
	}
}

func TestApplyDropsItemsThatAreNotManaged(t *testing.T) {
	env := jigtest.NewEnv()
	notManaged := &removeErrHandler{fakeHandler: newFake("ext"), err: jig.ErrNotManaged}
	j := newJig(t, env, _linux, jig.WithHandlers(notManaged), jig.WithRecord(_recordPath))
	m := &manifest.Manifest{Extras: []manifest.Extra{{Kind: "ext", Name: "stack"}}}
	applyPlan(t, j, j.Plan(mustStatus(t, j, m), jig.PlanOptions{}))

	results, err := j.Apply(t.Context(), j.PlanRemove(mustStatus(t, j, m), jig.RemoveOptions{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(results.Err(), jig.ErrNotManaged) {
		t.Fatalf("results error = %v, want ErrNotManaged", results.Err())
	}
	if got := recordedNames(t, j); len(got) != 0 {
		t.Errorf("recorded %v, want nothing", got)
	}
}

// removeErrHandler fails every removal with err.
type removeErrHandler struct {
	*fakeHandler
	err error
}

func (h *removeErrHandler) Remove(_ context.Context, _ jig.Env, _ manifest.Extra) error { return h.err }

func mustStatus(t *testing.T, j *jig.Jig, m *manifest.Manifest) []jig.Status {
	t.Helper()
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	return statuses
}

func TestPlanRemoveRecordedRemovesWhatTheManifestDropped(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	m := extrasManifest(env)
	if results, _ := applyAll(t, j, m); results.Err() != nil {
		t.Fatal(results.Err())
	}

	// The next manifest drops gh, and stack with it, and typescript.
	next := extrasManifest(env)
	delete(next.Tools, "gh")
	next.Extras = slices.DeleteFunc(next.Extras, func(e manifest.Extra) bool { return e.Name != "node" })
	plan, err := j.PlanRemoveRecorded(next, jig.RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := actionNames(plan); !slices.Equal(got, []string{"typescript", "stack", "gh"}) {
		t.Fatalf("plan = %v, want typescript, then stack before gh", got)
	}
	for _, a := range plan.Actions {
		if a.Op != jig.OpRemove {
			t.Errorf("%s op = %s, want remove", a.Name, a.Op)
		}
	}

	f.ext.calls, f.npm.calls = nil, nil
	applyPlan(t, j, plan)
	for _, path := range []string{"gh", "ghx"} {
		if env.FS.Exists(filepath.Join(_dirs.Bin, path)) {
			t.Errorf("%s was not removed", path)
		}
	}
	if !env.FS.Exists(filepath.Join(_dirs.Bin, "fnm")) {
		t.Error("fnm was removed, but the manifest still declares it")
	}
	if _, ok := f.ext.installed["stack"]; ok {
		t.Error("stack was not removed")
	}
	for _, call := range slices.Concat(f.ext.Calls(), f.npm.Calls()) {
		if strings.HasPrefix(call, "detect") {
			t.Errorf("removal asked the owning tool: %s", call)
		}
	}
	if got := recordedNames(t, j); !slices.Equal(got, []string{"fnm", "node"}) {
		t.Errorf("recorded %v, want fnm and node", got)
	}
}

func TestPlanRemoveRecordedWithoutAManifestRemovesEverything(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	if results, _ := applyAll(t, j, extrasManifest(env)); results.Err() != nil {
		t.Fatal(results.Err())
	}

	tests := []struct {
		name string
		opts jig.RemoveOptions
		want []string
	}{
		{"all", jig.RemoveOptions{}, []string{"typescript", "stack", "node", "gh", "fnm"}},
		{"extras", jig.RemoveOptions{Select: jig.Not(jig.OfKind(jig.KindTool))}, []string{"typescript", "stack", "node"}},
		{"named", jig.RemoveOptions{Select: jig.Named("node")}, []string{"node"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := j.PlanRemoveRecorded(nil, tt.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got := actionNames(plan); !slices.Equal(got, tt.want) {
				t.Errorf("plan = %v, want %v", got, tt.want)
			}
		})
	}

	plan, err := j.PlanRemoveRecorded(nil, jig.RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	applyPlan(t, j, plan)
	if got := recordedNames(t, j); len(got) != 0 {
		t.Errorf("recorded %v after removing everything", got)
	}
	if files := env.FS.Files(_dirs.Bin); len(files) != 0 {
		t.Errorf("bin still holds %v", files)
	}
}

func TestApplyReportsARecordThatCannotBeWritten(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	deps := env.Deps()
	deps.FS = failingWriteFS{FS: env.FS, path: _recordPath + ".jig-new"}
	j, err := jig.New(deps, _dirs, _linux, f.option(), jig.WithRecord(_recordPath))
	if err != nil {
		t.Fatal(err)
	}
	m := extrasManifest(env)

	results, err := j.Apply(t.Context(), j.Plan(mustStatus(t, j, m), jig.PlanOptions{}), nil)
	if err == nil || !strings.Contains(err.Error(), "update record") {
		t.Errorf("Apply() error = %v, want an update record error", err)
	}
	if results.Err() != nil || len(results) != 5 {
		t.Errorf("results = %+v, want five successful installs", results)
	}
}

type failingWriteFS struct {
	*jigtest.FS
	path string
}

func (f failingWriteFS) WriteFile(name string, data []byte, perm fs.FileMode) error {
	if name == f.path {
		return errors.New("disk full")
	}
	return f.FS.WriteFile(name, data, perm)
}

func TestApplyReplacesAnUnreadableRecord(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	env.FS.Seed(_recordPath, []byte("{"), 0o644)
	j := newJig(t, env, _linux, f.option(), jig.WithRecord(_recordPath))
	m := extrasManifest(env)
	stubInstalled(env, filepath.Join(_dirs.Bin, "fnm"), "fnm", "1.0.0")

	_, err := j.Apply(t.Context(), j.Plan(mustStatus(t, j, m), jig.PlanOptions{Select: jig.Named("gh")}), nil)
	if err == nil || !strings.Contains(err.Error(), "replaced unreadable record") {
		t.Errorf("Apply() error = %v, want the record reported as replaced", err)
	}
	if got := recordedNames(t, j); !slices.Equal(got, []string{"fnm", "gh"}) {
		t.Errorf("recorded %v, want fnm, found installed, and gh, installed", got)
	}
	if _, err := j.Apply(t.Context(), j.Plan(mustStatus(t, j, m), jig.PlanOptions{Select: jig.Named()}), nil); err != nil {
		t.Errorf("second Apply() error = %v, want the record repaired", err)
	}
}

func TestRecordRejectsAnUnreadableFile(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":       "{",
		"unknown format": `{"format": 99, "items": []}`,
	} {
		t.Run(name, func(t *testing.T) {
			env := jigtest.NewEnv()
			env.FS.Seed(_recordPath, []byte(content), 0o644)
			j := newJig(t, env, _linux, jig.WithRecord(_recordPath))
			_, err := j.Record()
			if err == nil || errors.Is(err, jig.ErrNoRecord) {
				t.Errorf("Record() error = %v, want a read error", err)
			}
		})
	}
}
