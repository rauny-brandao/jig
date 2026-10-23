# jig

jig is a Go library for installing pinned, checksum-verified developer tools from a declarative
manifest, along with the extras those tools manage: CLI extensions, language runtimes, global
packages.

> **Status:** v1.0. The API is stable: it changes only in backwards-compatible ways until v2.

## Why jig

Tools like aqua, mise and asdf are standalone CLIs. jig is meant to be **embedded in your own Go CLI**:

- **Verified and atomic.** Every download is checked against the SHA-256 in the manifest before
  unpacking. Archive entries, symlinks and hard links can never land outside the target folder, and
  binaries are written beside their destination and renamed into place so a failed install never
  leaves a broken binary behind.
- **Beyond binaries.** Extras are installed through the tool that owns them, after everything they
  `require`, and removed in reverse order. Five kinds are built in, and you can add your own.
- **Side-effect free planning.** `Status` never changes the machine, so a `Plan` doubles as a dry run.
- **Lifecycle tracking.** An optional install record tracks what jig manages, so it can prune items
  the manifest no longer declares, or uninstall everything, without asking the tools that own them.
- **Testable without I/O.** jig touches the filesystem, network and processes only through small
  interfaces: `host.Deps()` in production and in-memory fakes from `jigtest` in tests. Tested end to
  end on macOS, Linux and Windows.

## Install

```sh
go get github.com/rauny-brandao/jig@latest
```

API documentation and runnable examples are on [pkg.go.dev](https://pkg.go.dev/github.com/rauny-brandao/jig).

## Quickstart

### 1. Declare tools and extras

```yaml
tools:
  gh:
    version: "2.102.0"
    platforms:
      linux_amd64:
        url: "https://github.com/cli/cli/releases/download/v{version}/gh_{version}_linux_amd64.tar.gz"
        archive_format: tar.gz          # zip, tar.gz, tar.xz, tar.bz2, gz, binary or sdk
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

See [examples/tools.yaml](examples/tools.yaml) for a complete multi-platform manifest.

### 2. Check, plan and apply

```go
m, err := jig.Parse(data)
if err != nil { … }

dirs := jig.Dirs{Base: root, Bin: bin, Cache: cache, Home: home}
j, err := jig.New(host.Deps(), dirs, manifest.Current(), jig.WithHandlers(kinds.Defaults()...))
if err != nil { … }
if err := j.Validate(m); err != nil { … }

statuses, err := j.Status(ctx, m)                      // ok, newer, missing, outdated or unsupported
if err != nil { … }

plan := j.Plan(statuses, jig.PlanOptions{})            // no side effects: print it for a dry run
results, err := j.Apply(ctx, plan, func(e jig.Event) { // dependency order, parallel where possible
    fmt.Println(e.Type, e.Action.Op, e.Action.Name)
})
if err != nil { … }
if err := results.Err(); err != nil { … }
```

## Common workflows

### Filtering and pinning plans

`PlanOptions` narrows or widens what `Plan` schedules:

- **`Select`** limits the plan to matching items using `jig.Named("gh", "jq")`,
  `jig.OfKind(jig.KindTool)`, `jig.Not(...)` or any `func(jig.Status) bool` (`nil` means every item).
- **`Force`** reinstalls items already at the pin and downgrades newer ones to it.
- **`Downgrade`** replaces items newer than their pin with the pinned version when matched (`nil`
  leaves newer items alone):

  ```go
  plan := j.Plan(statuses, jig.PlanOptions{Downgrade: jig.OfKind("npm-global")})
  ```

Versions are compared as semantic versions (`v1.2.3` and `1.2.3` are equal); non-semantic versions
count as outdated unless they match the pin textually.

### Removing and pruning items

To remove currently installed items declared in a manifest, plan from `Status` (actions run in
reverse dependency order):

```go
teardown := j.PlanRemove(statuses, jig.RemoveOptions{
    Select: jig.Not(jig.OfKind(jig.KindTool)), // optional: remove only extras
})
```

To remove items a manifest **no longer declares**—or uninstall everything without probing the tools
that own the extras—configure an install record with `jig.WithRecord(path)`. `Apply` keeps the JSON
record up to date as items are detected, installed and removed:

```go
prune, err := j.PlanRemoveRecorded(m, jig.RemoveOptions{})   // items m no longer declares
all, err := j.PlanRemoveRecorded(nil, jig.RemoveOptions{})   // everything recorded
one, err := j.PlanRemoveRecorded(nil, jig.RemoveOptions{Select: jig.Named("jq")})
```

`j.Record()` lists the recorded entries. Both `PlanRemoveRecorded` and `Record` return an error
wrapping `jig.ErrNoRecord` until the first `Apply` writes the record.

### Keeping checksums current

```sh
go run github.com/rauny-brandao/jig/cmd/jig-checksums@latest -f tools.yaml          # update
go run github.com/rauny-brandao/jig/cmd/jig-checksums@latest -f tools.yaml -check   # verify in CI
```

`jig-checksums` downloads every asset and rewrites only the `sha256` fields, preserving comments and
formatting.

### Testing with `jigtest`

`jigtest` provides in-memory implementations of `FS`, `Fetcher`, `Extractor` and `Runner`:

```go
env := jigtest.NewEnv()
env.Fetcher.Serve(url, archiveBytes)
env.Extractor.Register(archiveBytes, map[string]jigtest.File{"gh_2.1.0/bin/gh": {Data: []byte("bin")}})
env.Runner.Stub(jig.CommandResult{Stdout: "gh version 2.1.0"}, ghPath, "version")

j, _ := jig.New(env.Deps(), dirs, manifest.Platform{OS: "linux", Arch: "amd64"})
```

## Reference

### Manifest fields

Additional `tools` fields:
- `extra_binaries` installs more files from the same archive (for example `uvx` next to `uv`).
- `sdk_dir` with `archive_format: sdk` keeps a whole SDK directory intact under `Dirs.Base`.
- `cleanup_paths` removes stale `Dirs.Home`-relative paths after a successful install.

Fields common to every item in `extras`:
- `requires` lists the tools and extras that must be installed first and removed last.
- `platforms` limits the extra to operating systems (`darwin`) or platform keys (`darwin_arm64`).
- `reinstall: always` reinstalls the extra on every apply, for sources that track a branch.
- `description` documents what the extra is for (ignored by jig).

### Built-in extra kinds

Register all built-in handlers with `jig.WithHandlers(kinds.Defaults()...)`. Each kind rejects
unknown fields during `Validate`:

| Kind | Installs | Through | Required fields | Optional fields |
|---|---|---|---|---|
| `gh-extension` | GitHub CLI extensions | `gh` | `repo` | — |
| `uv-tool` | Python applications into `Dirs.Bin` | `uv` | `source` | `hooks` (`post_install`, `pre_uninstall`) |
| `node-runtime` | A Node.js release channel | `fnm` | `channel` | `default` |
| `npm-global` | A global npm package (`--ignore-scripts`) | `fnm`, `npm` | — | `version`, `integrity` |
| `jdk` | A Java Development Kit | Coursier (`cs`) | — | `jvm` (defaults to `name`) |

> **Note:** Removing a `jdk` only deletes directories and archives inside Coursier's own caches. A
> JDK located anywhere else is left in place and fails removal with `jig.ErrNotManaged`.

### Options and custom kinds

Pass options to `jig.New`:

- `jig.WithHandlers(handlers...)` registers `kinds.Defaults()` or custom `jig.Handler` implementations.
- `jig.WithRecord(path)` maintains the JSON install record for `PlanRemoveRecorded` and `Record`.
- `jig.WithBeforeKind(kind, check)` runs a precondition once before the first action of `kind` in an
  `Apply` (for example, checking that `gh` is authenticated).
- `jig.WithPostInstall(name, hook)` runs a step after the named tool or extra installs.
- `jig.WithCommandsFromPath()` runs parent tools such as `gh` from `PATH` instead of managed
  binaries, for health checks that inspect what the user's shell runs.
- `jig.WithParallelism(n)` and `jig.WithTimeouts(probe, mutate)` tune concurrency and command
  timeouts.

## License

[Mozilla Public License 2.0](LICENSE). You can use jig in proprietary software. Changes to jig's own
files must be shared under the same license.
