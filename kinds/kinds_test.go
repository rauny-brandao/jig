// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package kinds_test

import (
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rauny-brandao/jig"
	"github.com/rauny-brandao/jig/jigtest"
	"github.com/rauny-brandao/jig/kinds"
	"github.com/rauny-brandao/jig/manifest"
)

var (
	_home = filepath.FromSlash("/home/dev")
	_dirs = jig.Dirs{
		Base: filepath.Join(_home, ".jig"), Bin: filepath.Join(_home, ".jig", "bin"),
		Cache: filepath.Join(_home, ".jig", "cache"), Home: _home,
	}
	_linux = manifest.Platform{OS: "linux", Arch: "amd64"}
)

// setup returns fakes and a handler environment for m on platform. Tools
// are not declared, so commands resolve to bare names such as "gh".
func setup(t *testing.T, platform manifest.Platform, m *manifest.Manifest) (*jigtest.Env, jig.Env) {
	t.Helper()
	env := jigtest.NewEnv()
	j, err := jig.New(env.Deps(), _dirs, platform, jig.WithHandlers(kinds.Defaults()...))
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		m = &manifest.Manifest{}
	}
	return env, j.Env(m)
}

func extra(kind, name string, params map[string]string) manifest.Extra {
	return manifest.Extra{Kind: kind, Name: name, Params: params}
}

func ok(stdout string) jig.CommandResult { return jig.CommandResult{Stdout: stdout} }

func failed(stderr string) jig.CommandResult { return jig.CommandResult{ExitCode: 1, Stderr: stderr} }

func calls(fake *jigtest.Env) []string {
	var lines []string
	for _, c := range fake.Runner.CallsWithEnv() {
		line := strings.Join(c.Args, " ")
		if len(c.Env) > 0 {
			line = strings.Join(c.Env, " ") + " " + line
		}
		lines = append(lines, line)
	}
	return lines
}

func TestDefaultsCoverEveryKind(t *testing.T) {
	var got []string
	for _, h := range kinds.Defaults() {
		got = append(got, string(h.Kind()))
	}
	if want := []string{"gh-extension", "uv-tool", "node-runtime", "npm-global", "jdk"}; !slices.Equal(got, want) {
		t.Errorf("Defaults() kinds = %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		h     jig.Handler
		extra manifest.Extra
		want  string
	}{
		{"gh repo required", kinds.GhExtension{}, extra("gh-extension", "stack", nil), "repo is required"},
		{"gh typo", kinds.GhExtension{}, extra("gh-extension", "stack", map[string]string{"repo": "a/b", "rpo": "a/b"}), `unknown field "rpo"`},
		{"gh hooks", kinds.GhExtension{}, manifest.Extra{Kind: "gh-extension", Name: "s", Params: map[string]string{"repo": "a/b"}, Hooks: manifest.Hooks{PostInstall: []string{"x"}}}, "does not support hooks"},
		{"uv source required", kinds.UVTool{}, extra("uv-tool", "app", nil), "source is required"},
		{"node channel required", kinds.NodeRuntime{}, extra("node-runtime", "node", nil), "channel is required"},
		{"node default value", kinds.NodeRuntime{}, extra("node-runtime", "node", map[string]string{"channel": "22", "default": "yes"}), "default must be true or false"},
		{"npm needs runtime", kinds.NPMGlobal{}, extra("npm-global", "typescript", nil), "must require"},
		{"jdk unknown field", kinds.JDK{}, extra("jdk", "temurin:25", map[string]string{"vendor": "x"}), `unknown field "vendor"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.h.Validate(tt.extra)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want %q", err, tt.want)
			}
		})
	}

	valid := []struct {
		h jig.Handler
		e manifest.Extra
	}{
		{kinds.GhExtension{}, extra("gh-extension", "stack", map[string]string{"repo": "github/gh-stack"})},
		{kinds.UVTool{}, manifest.Extra{Kind: "uv-tool", Name: "app", Params: map[string]string{"source": "app"}, Hooks: manifest.Hooks{PostInstall: []string{"--x"}}}},
		{kinds.NodeRuntime{}, extra("node-runtime", "node", map[string]string{"channel": "lts-latest", "default": "true"})},
		{kinds.NPMGlobal{}, manifest.Extra{Kind: "npm-global", Name: "typescript", Requires: []string{"node"}, Params: map[string]string{"version": "5.9.3"}}},
		{kinds.JDK{}, extra("jdk", "temurin:25", nil)},
	}
	for _, v := range valid {
		if err := v.h.Validate(v.e); err != nil {
			t.Errorf("%s: Validate() = %v", v.h.Kind(), err)
		}
	}
}

func TestGhExtension(t *testing.T) {
	stack := extra("gh-extension", "stack", map[string]string{"repo": "github/gh-stack"})
	dash := extra("gh-extension", "dash", map[string]string{"repo": "dlvhdr/gh-dash"})
	list := "gh stack\tGitHub/gh-stack\tv0.1.0\n"

	t.Run("detect batches one list", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok(list), "gh", "extension", "list")

		found, err := kinds.GhExtension{}.Detect(t.Context(), env, []manifest.Extra{stack, dash})
		if err != nil {
			t.Fatal(err)
		}
		if !found["stack"].Installed || found["dash"].Installed {
			t.Errorf("found = %+v", found)
		}
		if got := calls(fake); len(got) != 1 {
			t.Errorf("calls = %v, want one list", got)
		}
	})

	t.Run("detect reports a broken gh", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(failed("not logged in"), "gh", "extension", "list")
		if _, err := (kinds.GhExtension{}).Detect(t.Context(), env, []manifest.Extra{stack}); err == nil || !strings.Contains(err.Error(), "not logged in") {
			t.Errorf("Detect() error = %v", err)
		}
	})

	t.Run("install treats an existing extension as success", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(failed("already installed"), "gh", "extension", "install", "github/gh-stack")
		fake.Runner.Stub(ok(list), "gh", "extension", "list")
		if err := (kinds.GhExtension{}).Install(t.Context(), env, stack); err != nil {
			t.Errorf("Install() = %v, want nil", err)
		}
	})

	t.Run("install reports a real failure", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(failed("repository not found"), "gh", "extension", "install", "dlvhdr/gh-dash")
		fake.Runner.Stub(ok(list), "gh", "extension", "list")
		err := kinds.GhExtension{}.Install(t.Context(), env, dash)
		if _, isCmd := errors.AsType[*jig.CommandError](err); !isCmd || !strings.Contains(err.Error(), "repository not found") {
			t.Errorf("Install() = %v, want the gh error", err)
		}
	})

	t.Run("remove", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok(list), "gh", "extension", "list")
		fake.Runner.Stub(ok(""), "gh", "extension", "remove", "github/gh-stack")

		if err := (kinds.GhExtension{}).Remove(t.Context(), env, stack); err != nil {
			t.Errorf("Remove(installed) = %v", err)
		}
		if err := (kinds.GhExtension{}).Remove(t.Context(), env, dash); !errors.Is(err, jig.ErrNotInstalled) {
			t.Errorf("Remove(missing) = %v, want ErrNotInstalled", err)
		}
		if slices.Contains(calls(fake), "gh extension remove dlvhdr/gh-dash") {
			t.Error("tried to remove an extension that is not installed")
		}
	})
}

func TestUVTool(t *testing.T) {
	app := manifest.Extra{
		Kind: "uv-tool", Name: "app", Params: map[string]string{"source": "git+https://example.com/app.git"},
		Hooks: manifest.Hooks{PostInstall: []string{"--register"}, PreUninstall: []string{"--unregister"}},
	}
	list := "app v0.3.1\n- app\nother v1.0.0\n- app-helper\n"

	t.Run("detect", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok(list), "uv", "tool", "list")
		found, err := kinds.UVTool{}.Detect(t.Context(), env, []manifest.Extra{app, extra("uv-tool", "app-helper", nil)})
		if err != nil {
			t.Fatal(err)
		}
		if d := found["app"]; !d.Installed || d.Version != "0.3.1" {
			t.Errorf("app = %+v", d)
		}
		if found["app-helper"].Installed {
			t.Error("an executable name was taken for a tool")
		}
	})

	for _, platform := range []manifest.Platform{_linux, {OS: "windows", Arch: "amd64"}} {
		t.Run("install on "+platform.OS, func(t *testing.T) {
			fake, env := setup(t, platform, nil)
			hookBin := filepath.Join(_dirs.Bin, "app")
			if platform.OS == "windows" {
				hookBin += ".exe"
			}
			binDir := "UV_TOOL_BIN_DIR=" + _dirs.Bin
			fake.Runner.Stub(ok(""), "uv", "tool", "install", "git+https://example.com/app.git", "--reinstall")
			fake.Runner.Stub(ok(""), hookBin, "--register")

			if err := (kinds.UVTool{}).Install(t.Context(), env, app); err != nil {
				t.Fatal(err)
			}
			want := []string{
				binDir + " uv tool install git+https://example.com/app.git --reinstall",
				binDir + " " + hookBin + " --register",
			}
			if got := calls(fake); !slices.Equal(got, want) {
				t.Errorf("calls =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}

	t.Run("remove survives a failing pre_uninstall hook", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok(list), "uv", "tool", "list")
		fake.Runner.Stub(failed("already unregistered"), filepath.Join(_dirs.Bin, "app"), "--unregister")
		fake.Runner.Stub(ok(""), "uv", "tool", "uninstall", "app")

		if err := (kinds.UVTool{}).Remove(t.Context(), env, app); err != nil {
			t.Fatalf("Remove() = %v", err)
		}
		if got := calls(fake); !slices.Contains(got, "uv tool uninstall app") {
			t.Errorf("calls = %v", got)
		}
	})
}

func TestNodeRuntime(t *testing.T) {
	node := extra("node-runtime", "node", map[string]string{"channel": "lts-latest", "default": "true"})

	t.Run("detect", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok("* v22.11.0 default, lts-latest\n* system\n"), "fnm", "list")
		fake.Runner.Stub(ok("v22.11.0\n"), "fnm", "exec", "--using=lts-latest", "--", "node", "--version")
		found, err := kinds.NodeRuntime{}.Detect(t.Context(), env, []manifest.Extra{node, extra("node-runtime", "old", map[string]string{"channel": "18"})})
		if err != nil {
			t.Fatal(err)
		}
		if d := found["node"]; !d.Installed || d.Version != "22.11.0" {
			t.Errorf("node = %+v", d)
		}
		if found["old"].Installed {
			t.Error("unlisted channel reported installed")
		}
	})

	t.Run("install sets the default, best effort", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		fake.Runner.Stub(ok(""), "fnm", "install", "lts-latest")
		fake.Runner.Stub(failed("cannot set default"), "fnm", "default", "lts-latest")
		if err := (kinds.NodeRuntime{}).Install(t.Context(), env, node); err != nil {
			t.Fatalf("Install() = %v, want success despite the default failing", err)
		}
		if got := calls(fake); !slices.Equal(got, []string{"fnm install lts-latest", "fnm default lts-latest"}) {
			t.Errorf("calls = %v", got)
		}
	})
}

func TestNPMGlobal(t *testing.T) {
	node := extra("node-runtime", "node", map[string]string{"channel": "lts-latest"})
	ts := manifest.Extra{Kind: "npm-global", Name: "typescript", Requires: []string{"node"}, Params: map[string]string{"version": "5.9.3"}}
	m := &manifest.Manifest{Extras: []manifest.Extra{node, ts}}

	t.Run("detect reads the named package only", func(t *testing.T) {
		fake, env := setup(t, _linux, m)
		listing := "npm warn something\n" + `{"dependencies":{"typescript":{"version":"5.9.3"},"other":{"version":"9.9.9"}}}`
		fake.Runner.Stub(ok(listing), "fnm", "exec", "--using=lts-latest", "--", "npm", "list", "-g", "typescript", "--json")
		found, err := kinds.NPMGlobal{}.Detect(t.Context(), env, []manifest.Extra{ts})
		if err != nil {
			t.Fatal(err)
		}
		if d := found["typescript"]; !d.Installed || d.Version != "5.9.3" {
			t.Errorf("typescript = %+v", d)
		}
	})

	t.Run("missing package exits non-zero", func(t *testing.T) {
		fake, env := setup(t, _linux, m)
		fake.Runner.Stub(jig.CommandResult{ExitCode: 1, Stdout: "{}"}, "fnm", "exec", "--using=lts-latest", "--", "npm", "list", "-g", "typescript", "--json")
		found, err := kinds.NPMGlobal{}.Detect(t.Context(), env, []manifest.Extra{ts})
		if err != nil || found["typescript"].Installed {
			t.Errorf("Detect() = %+v, %v; want not installed", found, err)
		}
	})

	t.Run("install pins the version and uses npm.cmd on Windows", func(t *testing.T) {
		fake, env := setup(t, manifest.Platform{OS: "windows", Arch: "amd64"}, m)
		fake.Runner.Stub(ok(""), "fnm", "exec", "--using=lts-latest", "--", "npm.cmd", "install", "-g", "typescript@5.9.3")
		if err := (kinds.NPMGlobal{}).Install(t.Context(), env, ts); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("requires a node runtime", func(t *testing.T) {
		_, env := setup(t, _linux, &manifest.Manifest{Extras: []manifest.Extra{ts}})
		if err := (kinds.NPMGlobal{}).Install(t.Context(), env, ts); err == nil || !strings.Contains(err.Error(), "node-runtime") {
			t.Errorf("Install() = %v, want a missing runtime error", err)
		}
	})
}

func TestJDK(t *testing.T) {
	jdk := extra("jdk", "temurin:25", nil)
	arcHome := filepath.Join(_home, "Library", "Caches", "Coursier", "arc", "https", "github.com", "adoptium",
		"releases", "OpenJDK25U-jdk_aarch64_mac_hotspot_25.0.3_9.tar.gz", "jdk-25.0.3+9", "Contents", "Home")
	darwin := manifest.Platform{OS: "darwin", Arch: "arm64"}

	t.Run("detect never downloads", func(t *testing.T) {
		fake, env := setup(t, darwin, nil)
		fake.Runner.Stub(ok(arcHome+"\n"), "cs", "java-home", "--jvm", "temurin:25", "--mode", "offline")
		found, err := kinds.JDK{}.Detect(t.Context(), env, []manifest.Extra{jdk, extra("jdk", "zulu:8", nil)})
		if err != nil {
			t.Fatal(err)
		}
		if d := found["temurin:25"]; !d.Installed || d.Location != arcHome {
			t.Errorf("temurin = %+v", d)
		}
		if found["zulu:8"].Installed {
			t.Error("uncached JDK reported installed")
		}
		for _, c := range calls(fake) {
			if !strings.HasSuffix(c, "--mode offline") {
				t.Errorf("probe without offline mode: %s", c)
			}
		}
	})

	t.Run("the jvm field overrides the name", func(t *testing.T) {
		fake, env := setup(t, darwin, nil)
		fake.Runner.Stub(ok(""), "cs", "java-home", "--jvm", "temurin:21")
		if err := (kinds.JDK{}).Install(t.Context(), env, extra("jdk", "java", map[string]string{"jvm": "temurin:21"})); err != nil {
			t.Fatal(err)
		}
	})

	removal := []struct {
		name     string
		platform manifest.Platform
		vars     map[string]string
		home     string
		deleted  string
		download string // the archive Coursier downloaded, deleted too
	}{
		{"archive cache on macOS", darwin, nil, arcHome,
			filepath.Join(_home, "Library", "Caches", "Coursier", "arc", "https", "github.com", "adoptium", "releases", "OpenJDK25U-jdk_aarch64_mac_hotspot_25.0.3_9.tar.gz"),
			filepath.Join(_home, "Library", "Caches", "Coursier", "v1", "https", "github.com", "adoptium", "releases", "OpenJDK25U-jdk_aarch64_mac_hotspot_25.0.3_9.tar.gz")},
		{"jvm cache on Linux", _linux, nil,
			filepath.Join(_home, ".cache", "coursier", "jvm", "temurin@25", "bin", ".."),
			filepath.Join(_home, ".cache", "coursier", "jvm", "temurin@25"), ""},
		{"overridden jvm cache", _linux, map[string]string{"COURSIER_JVM_CACHE": filepath.FromSlash("/opt/jvms")},
			filepath.FromSlash("/opt/jvms/temurin@25/Contents/Home"), filepath.FromSlash("/opt/jvms/temurin@25"), ""},
		// Coursier reads COURSIER_ARCHIVE_CACHE; COURSIER_ARC_CACHE has no effect on it.
		{"overridden archive and download caches", _linux, map[string]string{"COURSIER_ARCHIVE_CACHE": filepath.FromSlash("/opt/arc"), "COURSIER_ARC_CACHE": filepath.FromSlash("/wrong"), "COURSIER_CACHE": filepath.FromSlash("/opt/dl")},
			filepath.FromSlash("/opt/arc/https/example.com/jdk.tar.gz/jdk-25/bin/.."), filepath.FromSlash("/opt/arc/https/example.com/jdk.tar.gz"),
			filepath.FromSlash("/opt/dl/https/example.com/jdk.tar.gz")},
		{"windows paths compare case-insensitively", manifest.Platform{OS: "windows", Arch: "amd64"},
			map[string]string{"LOCALAPPDATA": `C:\Users\Dev\AppData\Local`},
			`c:\users\dev\appdata\local\Coursier\Cache\jvm\temurin@25`,
			filepath.Join(`C:\Users\Dev\AppData\Local`, "Coursier", "Cache", "jvm", "temurin@25"), ""},
	}
	for _, tt := range removal {
		t.Run("remove from "+tt.name, func(t *testing.T) {
			fake, env := setup(t, tt.platform, nil)
			maps.Copy(fake.Vars, tt.vars)
			fake.Runner.Stub(ok(tt.home), "cs", "java-home", "--jvm", "temurin:25", "--mode", "offline")
			fake.FS.Seed(filepath.Join(tt.deleted, "release"), []byte("JAVA_VERSION=25"), 0)
			sibling := filepath.Join(filepath.Dir(tt.deleted), "keep", "file")
			fake.FS.Seed(sibling, []byte("x"), 0)
			var downloaded []string
			if tt.download != "" {
				checked := filepath.Join(filepath.Dir(tt.download), "."+filepath.Base(tt.download)+".checked")
				downloaded = []string{tt.download, checked}
				for _, p := range downloaded {
					fake.FS.Seed(p, []byte("x"), 0)
				}
				fake.FS.Seed(filepath.Join(filepath.Dir(tt.download), "other.tar.gz"), []byte("x"), 0)
			}

			if err := (kinds.JDK{}).Remove(t.Context(), env, jdk); err != nil {
				t.Fatalf("Remove() = %v", err)
			}
			if fake.FS.Exists(tt.deleted) {
				t.Errorf("%s not deleted", tt.deleted)
			}
			if !fake.FS.Exists(sibling) {
				t.Error("a sibling of the JDK was deleted")
			}
			for _, p := range downloaded {
				if fake.FS.Exists(p) {
					t.Errorf("download %s not deleted", p)
				}
			}
			if tt.download != "" && !fake.FS.Exists(filepath.Join(filepath.Dir(tt.download), "other.tar.gz")) {
				t.Error("another download was deleted")
			}
		})
	}

	t.Run("refuses a home outside Coursier's caches", func(t *testing.T) {
		fake, env := setup(t, _linux, nil)
		outside := filepath.FromSlash("/usr/lib/jvm/java-25")
		fake.Runner.Stub(ok(outside), "cs", "java-home", "--jvm", "temurin:25", "--mode", "offline")
		fake.FS.Seed(filepath.Join(outside, "release"), []byte("x"), 0)

		if err := (kinds.JDK{}).Remove(t.Context(), env, jdk); !errors.Is(err, jig.ErrNotManaged) || !strings.Contains(err.Error(), "not inside Coursier") {
			t.Fatalf("Remove() = %v, want an ErrNotManaged refusal", err)
		}
		if !fake.FS.Exists(outside) {
			t.Error("a JDK outside Coursier's caches was deleted")
		}
	})

	t.Run("remove of a missing JDK", func(t *testing.T) {
		_, env := setup(t, _linux, nil)
		if err := (kinds.JDK{}).Remove(t.Context(), env, jdk); !errors.Is(err, jig.ErrNotInstalled) {
			t.Errorf("Remove() = %v, want ErrNotInstalled", err)
		}
	})
}
