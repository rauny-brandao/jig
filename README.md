# jig

jig is a Go library for installing pinned, checksum-verified developer tools from a declarative
manifest, along with the extras those tools manage: CLI extensions, language runtimes, global
packages.

> **Status:** v0.3, pre-release. The API may change until v1.0.

## Why jig

Tools like aqua, mise and asdf are standalone CLIs. jig is meant to be **embedded in your own Go CLI**:

- **Pinned and verified.** Every download is checked against the SHA-256 in the manifest before
  anything is unpacked. Binaries are written beside their destination and renamed into place, so a
  failed install never leaves a broken binary behind.
- **Safe extraction.** Archive entries, symlinks and hard links can never land outside the target folder.
- **Beyond binaries.** Extras are installed through the tool that owns them, after everything they
  `require`, and removed in reverse order. Five kinds are built in, and you can add your own.
- **Side-effect free checks.** `Status` never changes the machine, so a plan doubles as a dry run.
- **Pluggable I/O.** jig touches the filesystem, network and processes only through small
  interfaces. `host.Deps()` gives you the real ones.
- **Testable.** `jigtest` ships in-memory fakes, so your tests never download or execute anything.
- **Cross-platform.** macOS, Linux and Windows, tested end to end on all three.

## Manifest

```yaml
tools:
  gh:
    version: "2.102.0"
    platforms:
      linux_amd64:
        url: "https://github.com/cli/cli/releases/download/v{version}/gh_{version}_linux_amd64.tar.gz"
        archive_format: tar.gz          # zip, tar.gz, tar.bz2, gz, binary or sdk
        binary_name: gh
        binary_path: "gh_{version}_linux_amd64/bin/gh"
        sha256: "bb766f710eef8ede859c18578c72c327597cd4c8a85b06001b1f3843c6019386"
    verify_command: ["gh", "version"]   # run against the installed binary
    verify_pattern: "gh version {version}"

extras:
  - { kind: gh-extension, name: branch, repo: mislav/gh-branch, requires: [gh] }
  - { kind: node-runtime, name: node, channel: lts-latest, requires: [fnm] }
  - { kind: npm-global, name: semver, version: "7.8.5", requires: [node] }
```

Other tool fields:
- `extra_binaries` installs more files from the same archive (for example `uvx` next to `uv`).
- `sdk_dir` with `archive_format: sdk` keeps a whole SDK directory intact.
- `cleanup_paths` removes stale home-relative paths after an install.

Fields common to every extra:
- `description` says what the extra is for. jig doesn't use it.
- `requires` lists the tools and extras it needs. They are installed first and removed last.
- `platforms` limits the extra to operating systems (`darwin`) or platform keys (`darwin_arm64`).
- `reinstall: always` reinstalls the extra on every apply, for sources that track a branch.

See [examples/tools.yaml](examples/tools.yaml) for a complete manifest.

### Built-in extra kinds

| Kind | Installs | Through | Fields |
|---|---|---|---|
| `gh-extension` | GitHub CLI extensions | `gh` | `repo` |
| `uv-tool` | Python applications, into jig's bin directory | `uv` | `source`; `hooks` |
| `node-runtime` | A Node.js release channel | `fnm` | `channel`, `default` |
| `npm-global` | A global npm package in a node-runtime | `fnm`, `npm` | `version` |
| `jdk` | A Java Development Kit | Coursier (`cs`) | `jvm` (defaults to the name) |

Each kind rejects fields it doesn't know, so typos fail validation. Removing a JDK only ever deletes
its directory inside Coursier's cache.

## Usage

```go
m, _ := jig.Parse(data)

j, _ := jig.New(host.Deps(), jig.Dirs{Base: root, Bin: bin, Cache: cache, Home: home},
    manifest.Current(), jig.WithHandlers(kinds.Defaults()...))
if err := j.Validate(m); err != nil { … }

statuses, _ := j.Status(ctx, m)                       // ok, missing, outdated or unsupported, per item
plan := j.Plan(statuses, jig.PlanOptions{})           // no side effects: print it for a dry run
results, _ := j.Apply(ctx, plan, func(e jig.Event) {  // dependency order, parallel where possible
    fmt.Println(e.Type, e.Action.Name)
})
if err := results.Err(); err != nil { … }

teardown := j.PlanRemove(statuses, jig.RemoveOptions{}) // reverse dependency order
```

Options:
- `jig.WithPostInstall(name, hook)` runs your own step after a tool or extra installs.
- `jig.WithBeforeKind(kind, check)` runs a precondition once before installing extras of a kind,
  such as checking that gh is logged in.
- `jig.WithHandlers(...)` registers custom kinds by implementing `jig.Handler`.
- `jig.WithCommandsFromPath()` makes handlers run parent tools such as `gh` from PATH instead of the
  managed binaries, for health checks that should see what the user's shell runs.
- `jig.WithParallelism(n)` and `jig.WithTimeouts(probe, mutate)` tune concurrency and timeouts.

### Testing code that uses jig

```go
env := jigtest.NewEnv()
env.Fetcher.Serve(url, archiveBytes)
env.Extractor.Register(archiveBytes, map[string]jigtest.File{"gh_2.1.0/bin/gh": {Data: []byte("bin")}})
env.Runner.Stub(jig.CommandResult{Stdout: "gh version 2.1.0"}, ghPath, "version")
j, _ := jig.New(env.Deps(), dirs, manifest.Platform{OS: "linux", Arch: "amd64"})
```

## Keeping checksums current

```sh
go run github.com/rauny-brandao/jig/cmd/jig-checksums@latest -f tools.yaml          # update
go run github.com/rauny-brandao/jig/cmd/jig-checksums@latest -f tools.yaml -check   # verify in CI
```

`jig-checksums` downloads every asset and rewrites only the `sha256` fields, keeping comments.

## Roadmap

| Version | Scope |
|---|---|
| v0.1 | Binaries: manifest, download and verify, safe extraction, Status / Plan / Apply, checksum updater |
| v0.2 | Extras: five built-in kinds, custom kinds, `requires` ordering, removal |
| v0.3 | Go 1.27, extra descriptions, running parent tools from PATH |
| v1.0 | Stable API |
| v1.1 | Install record: remove tools dropped from the manifest, uninstall a single tool |

## License

[Mozilla Public License 2.0](LICENSE). You can use jig in proprietary software. Changes to jig's own
files must be shared under the same license.
