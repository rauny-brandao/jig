// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/jigtest"
	"github.com/rauny-brandao/jig/manifest"
)

// fakeHandler keeps installed extras in memory and records every call.
type fakeHandler struct {
	kind jig.Kind

	mu         sync.Mutex
	installed  map[string]string // name -> version ("" for unversioned)
	calls      []string
	detectErr  error
	installErr map[string]error
	validate   func(manifest.Extra) error
	delay      time.Duration
	active     int
	maxActive  int
	commands   map[string]string
}

func newFake(kind jig.Kind) *fakeHandler {
	return &fakeHandler{kind: kind, installed: map[string]string{}, installErr: map[string]error{}, commands: map[string]string{}}
}

func (f *fakeHandler) Kind() jig.Kind { return f.kind }

func (f *fakeHandler) Validate(e manifest.Extra) error {
	if f.validate != nil {
		return f.validate(e)
	}
	return nil
}

func (f *fakeHandler) Detect(_ context.Context, env jig.Env, extras []manifest.Extra) (map[string]jig.Detected, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for _, e := range extras {
		names = append(names, e.Name)
	}
	f.calls = append(f.calls, "detect "+strings.Join(names, ","))
	f.commands["gh"], f.commands["missing-tool"] = env.Command("gh"), env.Command("missing-tool")
	if f.detectErr != nil {
		return nil, f.detectErr
	}
	found := map[string]jig.Detected{}
	for _, e := range extras {
		if v, ok := f.installed[e.Name]; ok {
			found[e.Name] = jig.Detected{Installed: true, Version: v, Location: "/opt/" + e.Name}
		}
	}
	return found, nil
}

func (f *fakeHandler) Install(_ context.Context, _ jig.Env, e manifest.Extra) error {
	f.enter("install " + e.Name)
	defer f.leave()
	if err := f.installErr[e.Name]; err != nil {
		return err
	}
	f.mu.Lock()
	f.installed[e.Name] = e.Param("version")
	f.mu.Unlock()
	return nil
}

func (f *fakeHandler) Remove(_ context.Context, _ jig.Env, e manifest.Extra) error {
	f.enter("remove " + e.Name)
	defer f.leave()
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.installed[e.Name]; !ok {
		return jig.ErrNotInstalled
	}
	delete(f.installed, e.Name)
	return nil
}

func (f *fakeHandler) enter(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	f.mu.Unlock()
	time.Sleep(f.delay)
}

func (f *fakeHandler) leave() {
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
}

func (f *fakeHandler) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// extrasManifest has two tools and a chain of extras:
// fnm <- node <- typescript, and gh <- stack.
func extrasManifest(env *jigtest.Env) *manifest.Manifest {
	return &manifest.Manifest{
		Tools: map[string]manifest.Tool{
			"fnm": archiveTool(env, "fnm", "1.0.0"),
			"gh":  archiveTool(env, "gh", "2.0.0"),
		},
		Extras: []manifest.Extra{
			{Kind: "npm", Name: "typescript", Requires: []string{"node"}, Params: map[string]string{"version": "5.0.0"}},
			{Kind: "runtime", Name: "node", Requires: []string{"fnm"}},
			{Kind: "ext", Name: "stack", Requires: []string{"gh"}},
		},
	}
}

type fakes struct{ npm, runtime, ext *fakeHandler }

func newFakes() fakes {
	return fakes{npm: newFake("npm"), runtime: newFake("runtime"), ext: newFake("ext")}
}

func (f fakes) option() jig.Option { return jig.WithHandlers(f.npm, f.runtime, f.ext) }

func stateOf(statuses []jig.Status) map[string]string {
	got := map[string]string{}
	for _, s := range statuses {
		got[s.Name] = string(s.State)
		if s.Have != "" {
			got[s.Name] += " " + s.Have
		}
	}
	return got
}

func TestStatusOfExtras(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option())
	m := extrasManifest(env)
	m.Extras = append(m.Extras,
		manifest.Extra{Kind: "ext", Name: "windows-only", Platforms: []string{"windows"}},
		manifest.Extra{Kind: "ext", Name: "needs-windows-only", Requires: []string{"windows-only"}},
	)
	stubInstalled(env, filepath.Join(_dirs.Bin, "gh"), "gh", "2.0.0")
	f.ext.installed["stack"] = ""

	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"fnm": "missing", "gh": "ok 2.0.0",
		"stack": "ok", "node": "missing", "typescript": "missing",
		"windows-only": "unsupported", "needs-windows-only": "unsupported",
	}
	if got := stateOf(statuses); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("states = %v\nwant     %v", got, want)
	}
	var order []string
	for _, s := range statuses {
		order = append(order, s.Name)
	}
	if want := []string{"fnm", "gh", "typescript", "node", "stack", "windows-only", "needs-windows-only"}; slices.Index(order, "node") > slices.Index(order, "typescript") || order[0] != want[0] {
		t.Errorf("order = %v: requirements must come first", order)
	}
	if calls := f.runtime.Calls(); len(calls) != 0 {
		t.Errorf("node was probed although fnm is missing: %v", calls)
	}
	if calls := f.ext.Calls(); !slices.Equal(calls, []string{"detect stack"}) {
		t.Errorf("ext calls = %v, want one batched detect of stack", calls)
	}
}

func TestStatusComparesExtraVersions(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option())
	m := extrasManifest(env)
	stubInstalled(env, filepath.Join(_dirs.Bin, "fnm"), "fnm", "1.0.0")
	f.runtime.installed["node"] = "22.1.0"

	for have, want := range map[string]jig.State{"4.9.9": jig.StateOutdated, "5.0.0": jig.StateOK, "5.1.0": jig.StateOK, "": jig.StateOK} {
		f.npm.installed["typescript"] = have
		statuses, err := j.Status(t.Context(), m)
		if err != nil {
			t.Fatal(err)
		}
		if s := statuses[slices.IndexFunc(statuses, func(s jig.Status) bool { return s.Name == "typescript" })]; s.State != want {
			t.Errorf("have %q: state = %s, want %s", have, s.State, want)
		}
	}
}

func TestStatusRecordsProbeFailures(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	f.ext.detectErr = errors.New("gh broke")
	m := extrasManifest(env)
	m.Extras = append(m.Extras, manifest.Extra{Kind: "unregistered", Name: "mystery", Requires: []string{"gh"}})
	stubInstalled(env, filepath.Join(_dirs.Bin, "gh"), "gh", "2.0.0")
	j := newJig(t, env, _linux, f.option())

	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range statuses {
		switch s.Name {
		case "stack":
			if s.State != jig.StateMissing || s.Err == nil || !strings.Contains(s.Err.Error(), "gh broke") {
				t.Errorf("stack = %s, %v; want missing with the probe error", s.State, s.Err)
			}
		case "mystery":
			if s.State != jig.StateMissing || !errors.Is(s.Err, jig.ErrUnknownKind) {
				t.Errorf("mystery = %s, %v; want missing with ErrUnknownKind", s.State, s.Err)
			}
		}
	}
}

func TestHandlersReachManagedTools(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option())
	stubInstalled(env, filepath.Join(_dirs.Bin, "gh"), "gh", "2.0.0")

	if _, err := j.Status(t.Context(), extrasManifest(env)); err != nil {
		t.Fatal(err)
	}
	if got := f.ext.commands["gh"]; got != filepath.Join(_dirs.Bin, "gh") {
		t.Errorf("Command(gh) = %q, want the managed binary", got)
	}
	if got := f.ext.commands["missing-tool"]; got != "missing-tool" {
		t.Errorf("Command(missing-tool) = %q, want the bare name", got)
	}
}

func TestValidateChecksKinds(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	f.ext.validate = func(e manifest.Extra) error {
		if e.Param("repo") == "" {
			return errors.New("repo is required")
		}
		return nil
	}
	m := extrasManifest(env)
	m.Extras = append(m.Extras, manifest.Extra{Kind: "nope", Name: "x"})
	j := newJig(t, env, _linux, f.option())

	err := j.Validate(m)
	if !errors.Is(err, jig.ErrUnknownKind) || !strings.Contains(err.Error(), `extra "stack": repo is required`) {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestPlanExtras(t *testing.T) {
	j := newJig(t, jigtest.NewEnv(), _linux)
	always := manifest.Extra{Name: "app", Reinstall: manifest.ReinstallAlways}
	statuses := []jig.Status{
		{Name: "fnm", Kind: jig.KindTool, State: jig.StateMissing},
		{Name: "node", Kind: "runtime", State: jig.StateMissing, Extra: manifest.Extra{Name: "node", Requires: []string{"fnm"}}},
		{Name: "app", Kind: "uv", State: jig.StateOK, Extra: always},
	}

	plan := j.Plan(statuses, jig.PlanOptions{})
	var got []string
	for _, a := range plan.Actions {
		got = append(got, string(a.Op)+" "+a.Name)
	}
	if want := []string{"install fnm", "install node", "reinstall app"}; !slices.Equal(got, want) {
		t.Errorf("Plan() = %v, want %v", got, want)
	}
	for _, a := range plan.Actions {
		if a.Blocked != nil {
			t.Errorf("%s blocked: %v", a.Name, a.Blocked)
		}
	}

	only := j.Plan(statuses, jig.PlanOptions{Only: []string{"node"}})
	if len(only.Actions) != 1 || !errors.Is(only.Actions[0].Blocked, jig.ErrRequirementMissing) {
		t.Errorf("node without fnm = %+v, want blocked by its missing requirement", only.Actions)
	}
}

// applyAll runs Status, Plan and Apply and returns the event log.
func applyAll(t *testing.T, j *jig.Jig, m *manifest.Manifest) (jig.Results, []string) {
	t.Helper()
	results, events := install(t, j, m)
	return results, eventTypes(events)
}

func TestApplyInstallsExtrasAfterTheirRequirements(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithParallelism(8))

	results, events := applyAll(t, j, extrasManifest(env))

	if err := results.Err(); err != nil {
		t.Fatal(err)
	}
	before := func(a, b string) {
		t.Helper()
		ia, ib := slices.Index(events, a), slices.Index(events, b)
		if ia < 0 || ib < 0 || ia > ib {
			t.Errorf("want %q before %q in %v", a, b, events)
		}
	}
	before("succeeded fnm", "started node")
	before("succeeded node", "started typescript")
	before("succeeded gh", "started stack")
	if f.npm.installed["typescript"] != "5.0.0" {
		t.Errorf("typescript installed as %q", f.npm.installed["typescript"])
	}
}

func TestApplyDoesNotInstallOnTopOfAFailedRequirement(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	f.runtime.installErr["node"] = errors.New("fnm install failed")
	j := newJig(t, env, _linux, f.option())

	results, events := applyAll(t, j, extrasManifest(env))

	for _, r := range results {
		switch r.Action.Name {
		case "node":
			if r.Err == nil {
				t.Error("node succeeded")
			}
		case "typescript":
			if !errors.Is(r.Err, jig.ErrRequirementFailed) {
				t.Errorf("typescript error = %v, want ErrRequirementFailed", r.Err)
			}
		case "stack", "gh", "fnm":
			if r.Err != nil {
				t.Errorf("%s failed: %v", r.Action.Name, r.Err)
			}
		}
	}
	if calls := f.npm.Calls(); slices.Contains(calls, "install typescript") {
		t.Error("typescript was installed although node failed")
	}
	if slices.Contains(events, "started typescript") {
		t.Errorf("typescript started: %v", events)
	}
}

func TestApplyFailsBlockedActionsWithoutRunningThem(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option())
	statuses, err := j.Status(t.Context(), extrasManifest(env))
	if err != nil {
		t.Fatal(err)
	}

	results, err := j.Apply(t.Context(), j.Plan(statuses, jig.PlanOptions{Only: []string{"node"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(results.Err(), jig.ErrRequirementMissing) {
		t.Errorf("Results.Err() = %v, want ErrRequirementMissing", results.Err())
	}
	if len(f.runtime.Calls()) != 0 {
		t.Errorf("blocked action ran: %v", f.runtime.Calls())
	}
}

func TestApplyRunsOneExtraOfAKindAtATime(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	f.ext.delay = 5 * time.Millisecond
	m := extrasManifest(env)
	for i := range 5 {
		m.Extras = append(m.Extras, manifest.Extra{Kind: "ext", Name: fmt.Sprintf("ext%d", i)})
	}
	j := newJig(t, env, _linux, f.option(), jig.WithParallelism(8))

	results, _ := applyAll(t, j, m)

	if err := results.Err(); err != nil {
		t.Fatal(err)
	}
	if f.ext.maxActive != 1 {
		t.Errorf("%d ext installs ran at once, want 1", f.ext.maxActive)
	}
}

func TestBeforeKindRunsOnceAndGatesInstalls(t *testing.T) {
	for _, failing := range []bool{false, true} {
		t.Run(fmt.Sprintf("failing=%v", failing), func(t *testing.T) {
			env := jigtest.NewEnv()
			f := newFakes()
			m := extrasManifest(env)
			m.Extras = append(m.Extras, manifest.Extra{Kind: "ext", Name: "dash"}, manifest.Extra{Kind: "ext", Name: "copilot"})
			var checks int
			var mu sync.Mutex
			j := newJig(t, env, _linux, f.option(), jig.WithBeforeKind("ext", func(context.Context) error {
				mu.Lock()
				defer mu.Unlock()
				checks++
				if failing {
					return errors.New("not logged in")
				}
				return nil
			}))

			results, _ := applyAll(t, j, m)

			if checks != 1 {
				t.Errorf("check ran %d times, want 1", checks)
			}
			for _, r := range results {
				if r.Action.Kind != "ext" {
					continue
				}
				if failing && (r.Err == nil || !strings.Contains(r.Err.Error(), "not logged in")) {
					t.Errorf("%s error = %v, want the check's error", r.Action.Name, r.Err)
				}
				if !failing && r.Err != nil {
					t.Errorf("%s failed: %v", r.Action.Name, r.Err)
				}
			}
			if failing && slices.ContainsFunc(f.ext.Calls(), func(c string) bool { return strings.HasPrefix(c, "install") }) {
				t.Errorf("ext installs ran despite the failed check: %v", f.ext.Calls())
			}
		})
	}
}

func TestPlanRemoveTearsDownInReverseOrder(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	j := newJig(t, env, _linux, f.option(), jig.WithParallelism(8))
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

	extrasOnly := j.PlanRemove(statuses, jig.RemoveOptions{ExtrasOnly: true})
	var names []string
	for _, a := range extrasOnly.Actions {
		names = append(names, a.Name)
		if a.Op != jig.OpRemove {
			t.Errorf("%s op = %s", a.Name, a.Op)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"node", "stack", "typescript"}) {
		t.Errorf("extras-only removal = %v, want node, stack and typescript", names)
	}
	order := extrasOnly.Actions
	if slices.IndexFunc(order, func(a jig.Action) bool { return a.Name == "typescript" }) >
		slices.IndexFunc(order, func(a jig.Action) bool { return a.Name == "node" }) {
		t.Error("typescript must be planned for removal before node, which it requires")
	}

	delete(f.ext.installed, "stack") // disappears between status and apply
	results, err := j.Apply(t.Context(), j.PlanRemove(statuses, jig.RemoveOptions{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := results.Err(); err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	calls := f.npm.Calls()
	if !slices.Contains(calls, "remove typescript") {
		t.Errorf("npm calls = %v", calls)
	}
	for _, r := range results {
		if r.Action.Name == "stack" && !r.Skipped {
			t.Error("removing an already missing extra was not reported as skipped")
		}
	}
	for _, bin := range []string{"gh", "ghx", "fnm", "fnmx"} {
		if env.FS.Exists(filepath.Join(_dirs.Bin, bin)) {
			t.Errorf("%s still installed", bin)
		}
	}
	if len(f.runtime.installed) != 0 || len(f.npm.installed) != 0 {
		t.Errorf("extras left: %v %v", f.runtime.installed, f.npm.installed)
	}
}

func TestRemoveWaitsForDependents(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	f.npm.delay = 10 * time.Millisecond
	j := newJig(t, env, _linux, f.option(), jig.WithParallelism(8))
	m := extrasManifest(env)
	f.npm.installed["typescript"] = "5.0.0"
	f.runtime.installed["node"] = ""
	stubInstalled(env, filepath.Join(_dirs.Bin, "fnm"), "fnm", "1.0.0")
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}

	var events []jig.Event
	if _, err := j.Apply(t.Context(), j.PlanRemove(statuses, jig.RemoveOptions{}), func(e jig.Event) { events = append(events, e) }); err != nil {
		t.Fatal(err)
	}
	log := eventTypes(events)
	if slices.Index(log, "succeeded typescript") > slices.Index(log, "started node") ||
		slices.Index(log, "succeeded node") > slices.Index(log, "started fnm") {
		t.Errorf("removal order = %v: dependents must be removed first", log)
	}
}

func TestRemoveToolDeletesSDKDirectory(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	tool := sdkTool(env, "2.0.0")
	asset, _ := tool.Asset(_linux.Key())
	stubInstalled(env, j.BinaryPath(asset), "sdk", "2.0.0")
	env.FS.Seed(filepath.Join(j.SDKPath(asset), "lib", "x"), []byte("x"), 0)
	m := manifestOf(map[string]manifest.Tool{"sdk": tool})
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}

	results, err := j.Apply(t.Context(), j.PlanRemove(statuses, jig.RemoveOptions{}), nil)
	if err != nil || results.Err() != nil {
		t.Fatalf("Apply() = %v, %v", err, results.Err())
	}
	if env.FS.Exists(j.SDKPath(asset)) {
		t.Error("SDK directory still exists")
	}
}

func TestApplyRunsPostInstallHooksForExtras(t *testing.T) {
	env := jigtest.NewEnv()
	f := newFakes()
	var hooked []string
	j := newJig(t, env, _linux, f.option(), jig.WithPostInstall("typescript", func(_ context.Context, in jig.Installed) error {
		hooked = append(hooked, in.Name+"@"+in.Version)
		return nil
	}))

	if results, _ := applyAll(t, j, extrasManifest(env)); results.Err() != nil {
		t.Fatal(results.Err())
	}
	if !slices.Equal(hooked, []string{"typescript@5.0.0"}) {
		t.Errorf("hooks = %v", hooked)
	}
}
