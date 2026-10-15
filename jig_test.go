// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package jig_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/jigtest"
	"github.com/rauny-brandao/jig/manifest"
)

var (
	_linux  = manifest.Platform{OS: "linux", Arch: "amd64"}
	_darwin = manifest.Platform{OS: "darwin", Arch: "arm64"}
	_home   = filepath.FromSlash("/home/dev")
	_dirs   = jig.Dirs{
		Base:  filepath.Join(_home, ".jig"),
		Bin:   filepath.Join(_home, ".jig", "bin"),
		Cache: filepath.Join(_home, ".jig", "cache"),
		Home:  _home,
	}
)

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func newJig(t *testing.T, env *jigtest.Env, platform manifest.Platform, opts ...jig.Option) *jig.Jig {
	t.Helper()
	j, err := jig.New(env.Deps(), _dirs, platform, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return j
}

// archiveTool returns a tar.gz tool whose archive is registered with env:
// <name>_<version>/bin/<name> plus an extra binary <name>x.
func archiveTool(env *jigtest.Env, name, ver string) manifest.Tool {
	body := []byte("archive of " + name + " " + ver)
	url := "https://example.com/" + name + "/v{version}/" + name + "_{version}.tar.gz"
	env.Fetcher.Serve(strings.ReplaceAll(url, "{version}", ver), body)
	env.Extractor.Register(body, map[string]jigtest.File{
		name + "_" + ver + "/bin/" + name:       {Data: []byte(name + " binary " + ver), Mode: 0o755},
		name + "_" + ver + "/bin/" + name + "x": {Data: []byte(name + "x binary " + ver), Mode: 0o755},
		name + "_" + ver + "/README":            {Data: []byte("readme")},
	})
	asset := manifest.Asset{
		URL:           url,
		ArchiveFormat: manifest.FormatTarGz,
		BinaryName:    name,
		BinaryPath:    name + "_{version}/bin/" + name,
		ExtraBinaries: []string{name + "_{version}/bin/" + name + "x"},
		SHA256:        digest(body),
	}
	return manifest.Tool{
		Version:       ver,
		Platforms:     map[string]manifest.Asset{"linux_amd64": asset, "darwin_arm64": asset},
		VerifyCommand: []string{name, "--version"},
		VerifyPattern: name + " version {version}",
	}
}

func manifestOf(tools map[string]manifest.Tool) *manifest.Manifest {
	return &manifest.Manifest{Tools: tools}
}

// stubInstalled makes the managed binary at path report ver.
func stubInstalled(env *jigtest.Env, path, name, ver string) {
	env.FS.Seed(path, []byte("installed"), 0o755)
	env.Runner.Stub(jig.CommandResult{Stdout: name + " version " + ver + "\n"}, path, "--version")
}

func TestNewRequiresDepsDirsAndPlatform(t *testing.T) {
	_, err := jig.New(jig.Deps{}, jig.Dirs{}, manifest.Platform{})
	if err == nil {
		t.Fatal("New() succeeded with nothing configured")
	}
	for _, want := range []string{"FS", "Fetcher", "Extractor", "Runner", "dirs", "platform"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestStatusReportsEachState(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)

	unsupported := archiveTool(env, "winonly", "1.0.0")
	unsupported.Platforms = map[string]manifest.Asset{"windows_amd64": unsupported.Platforms["linux_amd64"]}
	broken := archiveTool(env, "broken", "1.0.0")
	m := manifestOf(map[string]manifest.Tool{
		"current":  archiveTool(env, "current", "2.0.0"),
		"newer":    archiveTool(env, "newer", "2.0.0"),
		"old":      archiveTool(env, "old", "2.0.0"),
		"absent":   archiveTool(env, "absent", "2.0.0"),
		"broken":   broken,
		"winonly":  unsupported,
		"unparsed": archiveTool(env, "unparsed", "2.0.0"),
	})
	stubInstalled(env, filepath.Join(_dirs.Bin, "current"), "current", "2.0.0")
	stubInstalled(env, filepath.Join(_dirs.Bin, "newer"), "newer", "2.1.0")
	stubInstalled(env, filepath.Join(_dirs.Bin, "old"), "old", "1.9.9")
	env.FS.Seed(filepath.Join(_dirs.Bin, "broken"), []byte("x"), 0o755)
	env.Runner.Stub(jig.CommandResult{ExitCode: 1}, filepath.Join(_dirs.Bin, "broken"), "--version")
	env.FS.Seed(filepath.Join(_dirs.Bin, "unparsed"), []byte("x"), 0o755)
	env.Runner.Stub(jig.CommandResult{Stdout: "something else"}, filepath.Join(_dirs.Bin, "unparsed"), "--version")

	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	got := map[string]string{}
	var names []string
	for _, s := range statuses {
		names = append(names, s.Name)
		got[s.Name] = string(s.State) + " " + s.Have
		if s.Kind != jig.KindTool {
			t.Errorf("%s kind = %q", s.Name, s.Kind)
		}
	}
	want := map[string]string{
		"current":  "ok 2.0.0",
		"newer":    "ok 2.1.0",
		"old":      "outdated 1.9.9",
		"absent":   "missing ",
		"broken":   "missing ",
		"unparsed": "missing ",
		"winonly":  "unsupported ",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
	if !slices.IsSorted(names) {
		t.Errorf("statuses not sorted by name: %v", names)
	}
}

func TestStatusDoesNotRunMissingBinaries(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)

	if _, err := j.Status(t.Context(), manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.0.0")})); err != nil {
		t.Fatal(err)
	}
	if calls := env.Runner.Calls(); len(calls) != 0 {
		t.Errorf("ran %v for a binary that does not exist", calls)
	}
}

func TestStatusChecksTheSDKBinary(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	tool := sdkTool(env, "2.0.0")
	asset, _ := tool.Asset(_linux.Key())
	stubInstalled(env, j.BinaryPath(asset), "sdk", "2.0.0")

	statuses, err := j.Status(t.Context(), manifestOf(map[string]manifest.Tool{"sdk": tool}))
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != jig.StateOK {
		t.Errorf("state = %q, want ok", statuses[0].State)
	}
	if want := filepath.Join(_dirs.Base, "my-sdk", "bin", "sdk"); j.BinaryPath(asset) != want {
		t.Errorf("BinaryPath = %q, want %q", j.BinaryPath(asset), want)
	}
}

func TestStatusRejectsNilManifest(t *testing.T) {
	if _, err := newJig(t, jigtest.NewEnv(), _linux).Status(t.Context(), nil); err == nil {
		t.Fatal("Status(nil) succeeded")
	}
}

func TestPlan(t *testing.T) {
	statuses := []jig.Status{
		{Name: "a", Kind: jig.KindTool, State: jig.StateMissing, Want: "1.0.0"},
		{Name: "b", Kind: jig.KindTool, State: jig.StateOutdated, Have: "0.9.0", Want: "1.0.0"},
		{Name: "c", Kind: jig.KindTool, State: jig.StateOK, Have: "1.0.0", Want: "1.0.0"},
		{Name: "d", Kind: jig.KindTool, State: jig.StateUnsupported, Want: "1.0.0"},
	}
	j := newJig(t, jigtest.NewEnv(), _linux)

	tests := []struct {
		name string
		opts jig.PlanOptions
		want []string
	}{
		{"default", jig.PlanOptions{}, []string{"install a", "upgrade b 0.9.0->1.0.0"}},
		{"force", jig.PlanOptions{Force: true}, []string{"install a", "upgrade b 0.9.0->1.0.0", "reinstall c 1.0.0->1.0.0"}},
		{"only", jig.PlanOptions{Only: []string{"b", "d"}}, []string{"upgrade b 0.9.0->1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, a := range j.Plan(statuses, tt.opts).Actions {
				s := string(a.Op) + " " + a.Name
				if a.From != "" {
					s += " " + a.From + "->" + a.To
				}
				got = append(got, s)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("Plan() = %q, want %q", got, tt.want)
			}
		})
	}
	if !j.Plan(statuses[2:], jig.PlanOptions{}).Empty() {
		t.Error("plan for up-to-date and unsupported items is not empty")
	}
}

// install runs Status, Plan and Apply for m and returns the results and the
// events emitted.
func install(t *testing.T, j *jig.Jig, m *manifest.Manifest) (jig.Results, []jig.Event) {
	t.Helper()
	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	var events []jig.Event
	results, err := j.Apply(t.Context(), j.Plan(statuses, jig.PlanOptions{}), func(e jig.Event) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	return results, events
}

func TestApplyInstallsFromArchive(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)

	results, events := install(t, j, manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.1.0")}))

	if err := results.Err(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}
	assertBinary(t, env, filepath.Join(_dirs.Bin, "gh"), "gh binary 2.1.0")
	assertBinary(t, env, filepath.Join(_dirs.Bin, "ghx"), "ghx binary 2.1.0")
	if files := env.FS.Files(_dirs.Cache); len(files) != 0 {
		t.Errorf("cache not cleaned up: %v", files)
	}
	if files := env.FS.Files(_dirs.Bin); len(files) != 2 {
		t.Errorf("bin contains %v, want only gh and ghx", files)
	}
	calls := env.Extractor.Calls()
	if len(calls) != 1 || calls[0].Format != "tar.gz" || filepath.Base(calls[0].ArchivePath) != "gh_2.1.0.tar.gz" {
		t.Errorf("extractor calls = %+v", calls)
	}
	if got := eventTypes(events); !slices.Equal(got, []string{"started gh", "succeeded gh"}) {
		t.Errorf("events = %v", got)
	}
}

func TestApplyInstallsARawBinary(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	body := []byte("raw kubectl")
	env.Fetcher.Serve("https://example.com/kubectl/1.0.0/kubectl", body)
	tool := manifest.Tool{
		Version: "1.0.0",
		Platforms: map[string]manifest.Asset{"linux_amd64": {
			URL: "https://example.com/kubectl/{version}/kubectl", ArchiveFormat: manifest.FormatBinary,
			BinaryName: "kubectl", SHA256: digest(body),
		}},
		VerifyCommand: []string{"kubectl", "version"},
	}

	results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"kubectl": tool}))

	if err := results.Err(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}
	assertBinary(t, env, filepath.Join(_dirs.Bin, "kubectl"), "raw kubectl")
	if calls := env.Extractor.Calls(); len(calls) != 0 {
		t.Errorf("extractor used for a raw binary: %+v", calls)
	}
}

func TestApplyRemovesQuarantineOnlyOnMacOS(t *testing.T) {
	for _, platform := range []manifest.Platform{_darwin, _linux, {OS: "windows", Arch: "amd64"}} {
		t.Run(platform.Key(), func(t *testing.T) {
			env := jigtest.NewEnv()
			j := newJig(t, env, platform)
			tool := archiveTool(env, "gh", "2.1.0")
			tool.Platforms[platform.Key()] = tool.Platforms["linux_amd64"]

			results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"gh": tool}))
			if err := results.Err(); err != nil {
				t.Fatal(err)
			}

			var xattr [][]string
			for _, call := range env.Runner.Calls() {
				if call[0] == "xattr" {
					xattr = append(xattr, call)
				}
			}
			if platform.OS != "darwin" {
				if len(xattr) != 0 {
					t.Errorf("xattr ran on %s: %v", platform.OS, xattr)
				}
				return
			}
			want := []string{"xattr", "-d", "com.apple.quarantine", filepath.Join(_dirs.Bin, "gh")}
			if len(xattr) != 2 || !slices.Equal(xattr[0], want) {
				t.Errorf("xattr calls = %v, want %v and one for ghx", xattr, want)
			}
		})
	}
}

func sdkTool(env *jigtest.Env, ver string) manifest.Tool {
	body := []byte("sdk archive " + ver)
	env.Fetcher.Serve("https://example.com/sdk-"+ver+".zip", body)
	env.Extractor.Register(body, map[string]jigtest.File{
		"my-sdk/bin/sdk":     {Data: []byte("sdk " + ver), Mode: 0o755},
		"my-sdk/lib/core.py": {Data: []byte("core " + ver)},
	})
	asset := manifest.Asset{
		URL: "https://example.com/sdk-{version}.zip", ArchiveFormat: manifest.FormatSDK,
		BinaryName: "sdk", SDKDir: "my-sdk", SHA256: digest(body),
	}
	return manifest.Tool{
		Version:       ver,
		Platforms:     map[string]manifest.Asset{"linux_amd64": asset},
		VerifyCommand: []string{"sdk", "--version"},
		VerifyPattern: "sdk version {version}",
	}
}

func TestApplyInstallsAndReplacesAnSDK(t *testing.T) {
	env := jigtest.NewEnv()
	var hooked []jig.Installed
	j := newJig(t, env, _linux, jig.WithPostInstall("sdk", func(_ context.Context, in jig.Installed) error {
		hooked = append(hooked, in)
		return nil
	}))
	sdkDir := filepath.Join(_dirs.Base, "my-sdk")
	env.FS.Seed(filepath.Join(sdkDir, "stale.txt"), []byte("from the old version"), 0)

	results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"sdk": sdkTool(env, "2.0.0")}))

	if err := results.Err(); err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}
	want := []string{filepath.Join(sdkDir, "bin", "sdk"), filepath.Join(sdkDir, "lib", "core.py")}
	if got := env.FS.Files(sdkDir); !slices.Equal(got, want) {
		t.Errorf("SDK files = %v, want %v", got, want)
	}
	if env.FS.Exists(sdkDir + ".jig-old") {
		t.Error("backup of the previous SDK was not removed")
	}
	if calls := env.Extractor.Calls(); len(calls) != 1 || calls[0].Format != "zip" {
		t.Errorf("extractor calls = %+v, want one zip extraction", calls)
	}
	if len(hooked) != 1 || hooked[0].Path != sdkDir || hooked[0].Version != "2.0.0" {
		t.Errorf("post-install hook got %+v", hooked)
	}
}

func TestApplyKeepsThePreviousSDKWhenTheArchiveIsWrong(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	tool := sdkTool(env, "2.0.0")
	asset := tool.Platforms["linux_amd64"]
	asset.SDKDir = "other-dir"
	tool.Platforms["linux_amd64"] = asset
	previous := filepath.Join(_dirs.Base, "other-dir", "bin", "sdk")
	env.FS.Seed(previous, []byte("previous"), 0o755)

	results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"sdk": tool}))

	if err := results.Err(); err == nil || !strings.Contains(err.Error(), "other-dir") {
		t.Fatalf("Apply() error = %v, want missing directory", err)
	}
	assertBinary(t, env, previous, "previous")
}

// recordingVerifier is a verifying fetcher that records whether jig used
// streaming verification.
type recordingVerifier struct {
	jigtest.VerifyingFetcher
	verified int
}

func (r *recordingVerifier) DownloadVerified(ctx context.Context, url, dest, sha string) error {
	r.verified++
	return r.VerifyingFetcher.DownloadVerified(ctx, url, dest, sha)
}

func TestApplyRejectsChecksumMismatchAndKeepsTheOldBinary(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			env := jigtest.NewEnv()
			deps := env.Deps()
			verifier := &recordingVerifier{Fetcher: env.Fetcher}
			if streaming {
				deps.Fetcher = verifier
			}
			j, err := jig.New(deps, _dirs, _linux)
			if err != nil {
				t.Fatal(err)
			}
			tool := archiveTool(env, "gh", "2.1.0")
			env.Fetcher.Serve("https://example.com/gh/v2.1.0/gh_2.1.0.tar.gz", []byte("tampered"))
			old := filepath.Join(_dirs.Bin, "gh")
			env.FS.Seed(old, []byte("old gh"), 0o755)

			results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"gh": tool}))

			if err := results.Err(); !errors.Is(err, jig.ErrChecksumMismatch) {
				t.Fatalf("Apply() error = %v, want ErrChecksumMismatch", err)
			}
			if streaming && verifier.verified != 1 {
				t.Errorf("DownloadVerified called %d times, want 1", verifier.verified)
			}
			assertBinary(t, env, old, "old gh")
			if calls := env.Extractor.Calls(); len(calls) != 0 {
				t.Errorf("unverified download was extracted: %+v", calls)
			}
			if files := env.FS.Files(_dirs.Cache); len(files) != 0 {
				t.Errorf("cache not cleaned up: %v", files)
			}
		})
	}
}

func TestApplyRejectsAnInvalidChecksumWithoutDownloading(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	tool := archiveTool(env, "gh", "2.1.0")
	asset := tool.Platforms["linux_amd64"]
	asset.SHA256 = ""
	tool.Platforms["linux_amd64"] = asset

	results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"gh": tool}))

	if err := results.Err(); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("Apply() error = %v, want sha256 error", err)
	}
	if calls := env.Fetcher.Calls(); len(calls) != 0 {
		t.Errorf("downloaded %v despite an invalid checksum", calls)
	}
}

func TestApplyContinuesAfterAFailure(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	m := manifestOf(map[string]manifest.Tool{
		"bad":  archiveTool(env, "bad", "1.0.0"),
		"good": archiveTool(env, "good", "1.0.0"),
	})
	env.Fetcher.Fail("https://example.com/bad/v1.0.0/bad_1.0.0.tar.gz", errors.New("connection reset"))

	results, events := install(t, j, m)

	err := results.Err()
	if err == nil || !strings.Contains(err.Error(), "install bad") || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("Results.Err() = %v", err)
	}
	if results[1].Action.Name != "good" || results[1].Err != nil {
		t.Errorf("good result = %+v", results[1])
	}
	assertBinary(t, env, filepath.Join(_dirs.Bin, "good"), "good binary 1.0.0")
	if !slices.Contains(eventTypes(events), "failed bad") {
		t.Errorf("events = %v, want a failed event for bad", eventTypes(events))
	}
}

func TestApplyCleansUpHomePaths(t *testing.T) {
	for _, home := range []string{_home, ""} {
		t.Run("home="+home, func(t *testing.T) {
			env := jigtest.NewEnv()
			dirs := _dirs
			dirs.Home = home
			j, err := jig.New(env.Deps(), dirs, _linux)
			if err != nil {
				t.Fatal(err)
			}
			tool := archiveTool(env, "gh", "2.1.0")
			tool.CleanupPaths = []string{".gh/pkg", "../escape"}
			stale := filepath.Join(_home, ".gh", "pkg", "old")
			outside := filepath.Join(filepath.Dir(_home), "escape")
			env.FS.Seed(stale, []byte("stale"), 0)
			env.FS.Seed(outside, []byte("not ours"), 0)

			results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"gh": tool}))
			if err := results.Err(); err != nil {
				t.Fatal(err)
			}

			if removed := !env.FS.Exists(stale); removed != (home != "") {
				t.Errorf("stale path removed = %v, want %v", removed, home != "")
			}
			if !env.FS.Exists(outside) {
				t.Error("a path outside the home directory was removed")
			}
		})
	}
}

func TestApplyFailsWhenThePostInstallHookFails(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux, jig.WithPostInstall("gh", func(context.Context, jig.Installed) error {
		return errors.New("hook broke")
	}))

	results, _ := install(t, j, manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.1.0")}))

	if err := results.Err(); err == nil || !strings.Contains(err.Error(), "hook broke") {
		t.Fatalf("Apply() error = %v, want hook error", err)
	}
}

func TestApplyRunsManyToolsInParallel(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux, jig.WithParallelism(4))
	tools := map[string]manifest.Tool{}
	for i := range 20 {
		name := fmt.Sprintf("tool%02d", i)
		tools[name] = archiveTool(env, name, "1.0.0")
	}

	results, events := install(t, j, manifestOf(tools))

	if err := results.Err(); err != nil {
		t.Fatal(err)
	}
	if len(results) != 20 || len(events) != 40 {
		t.Errorf("got %d results and %d events, want 20 and 40", len(results), len(events))
	}
	for i, r := range results {
		if want := fmt.Sprintf("tool%02d", i); r.Action.Name != want {
			t.Errorf("results[%d] = %s, want %s (plan order)", i, r.Action.Name, want)
		}
	}
}

func TestApplyCallsOnEventSerially(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux, jig.WithParallelism(8))
	tools := map[string]manifest.Tool{}
	for i := range 8 {
		name := fmt.Sprintf("tool%d", i)
		tools[name] = archiveTool(env, name, "1.0.0")
	}
	statuses, err := j.Status(t.Context(), manifestOf(tools))
	if err != nil {
		t.Fatal(err)
	}

	var inside, overlaps int
	var mu sync.Mutex
	_, err = j.Apply(t.Context(), j.Plan(statuses, jig.PlanOptions{}), func(jig.Event) {
		inside++ // unsynchronised on purpose: the race detector flags concurrent calls
		if inside > 1 {
			mu.Lock()
			overlaps++
			mu.Unlock()
		}
		inside--
	})
	if err != nil {
		t.Fatal(err)
	}
	if overlaps != 0 {
		t.Errorf("onEvent overlapped %d times", overlaps)
	}
}

func TestApplyStopsWhenTheContextIsCancelled(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	statuses, err := j.Status(t.Context(), manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.1.0")}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	results, err := j.Apply(ctx, j.Plan(statuses, jig.PlanOptions{}), nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Apply() error = %v, want context.Canceled", err)
	}
	if results.Err() == nil {
		t.Error("cancelled action reported success")
	}
	if env.FS.Exists(filepath.Join(_dirs.Bin, "gh")) {
		t.Error("tool installed after cancellation")
	}
}

func TestStatusAfterApplyIsOK(t *testing.T) {
	env := jigtest.NewEnv()
	j := newJig(t, env, _linux)
	m := manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.1.0")})
	results, _ := install(t, j, m)
	if err := results.Err(); err != nil {
		t.Fatal(err)
	}
	env.Runner.Stub(jig.CommandResult{Stdout: "gh version 2.1.0"}, filepath.Join(_dirs.Bin, "gh"), "--version")

	statuses, err := j.Status(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	if statuses[0].State != jig.StateOK {
		t.Errorf("state after install = %q, want ok", statuses[0].State)
	}
	if !j.Plan(statuses, jig.PlanOptions{}).Empty() {
		t.Error("plan after install is not empty")
	}
}

func assertBinary(t *testing.T, env *jigtest.Env, path, content string) {
	t.Helper()
	data, mode, ok := env.FS.File(path)
	if !ok {
		t.Fatalf("%s not installed", path)
	}
	if string(data) != content {
		t.Errorf("%s = %q, want %q", path, data, content)
	}
	if mode != fs.FileMode(0o755) {
		t.Errorf("%s mode = %v, want 0755", path, mode)
	}
}

func eventTypes(events []jig.Event) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = string(e.Type) + " " + e.Action.Name
	}
	return types
}

// strictRemoveFS rejects removing a missing path, as some Filesystem
// implementations do; jig must not depend on Remove tolerating it.
type strictRemoveFS struct{ *jigtest.FS }

func (s strictRemoveFS) Remove(path string) error {
	if !s.Exists(path) {
		return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
	}
	return s.FS.Remove(path)
}

func TestApplyWorksWithAStrictRemove(t *testing.T) {
	env := jigtest.NewEnv()
	deps := env.Deps()
	deps.FS = strictRemoveFS{env.FS}
	j, err := jig.New(deps, _dirs, _linux)
	if err != nil {
		t.Fatal(err)
	}
	m := manifestOf(map[string]manifest.Tool{"gh": archiveTool(env, "gh", "2.1.0"), "sdk": sdkTool(env, "2.0.0")})

	results, _ := install(t, j, m)

	if err := results.Err(); err != nil {
		t.Fatalf("Apply() with a strict Remove failed: %v", err)
	}
}
