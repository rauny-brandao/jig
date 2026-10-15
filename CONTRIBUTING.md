# Contributing

Bug reports and ideas are welcome as [issues](https://github.com/rauny-brandao/jig/issues). Pull
requests are limited to collaborators. To report a security problem, see [SECURITY.md](SECURITY.md).

## Setup

You need Go 1.27 or newer and [just](https://github.com/casey/just). golangci-lint and govulncheck run
through `go run` at pinned versions, so you don't need to install them.

```sh
just check   # build, test, lint, hygiene: the same checks as CI
just test    # tests only
JIG_E2E=1 go test -run TestEndToEnd .   # real install and removal of examples/tools.yaml (sandboxed)
JIG_E2E=1 JIG_E2E_JDK=1 go test -run TestEndToEnd .   # the same, including the JDK (a large download)
```

On Windows, run the recipes from Git Bash or WSL.

## Rules

- **License header.** Every Go file starts with the MPL-2.0 header (lint enforces this):

  ```go
  // This Source Code Form is subject to the terms of the Mozilla Public
  // License, v. 2.0. If a copy of the MPL was not distributed with this
  // file, You can obtain one at https://mozilla.org/MPL/2.0/.
  ```

- **No I/O outside the interfaces.** Packages reach the filesystem, network and processes only through
  the interfaces the caller passes in. Tests use the fakes in `jigtest`, never the real network.
- **Public examples only.** Manifests, fixtures and docs use publicly available tools and URLs. Never
  include credentials, internal hostnames or company-specific names.
- **Commits.** Use conventional commit titles (`feat:`, `fix:`, `docs:`, `chore:` …).

## Hygiene checks

CI runs two checks on every push and pull request:

- **gitleaks** scans the full history for secrets.
- **Forbidden strings** fails if tracked files or commit metadata match any pattern on a private list.
  The list is never committed. CI reads it from the `FORBIDDEN_PATTERNS` repository secret, written on
  one line as `a|b|c` so GitHub masks it in logs. Findings are reported as `file:line` or a commit hash,
  never the matched text. To run the check locally, put the same patterns in `.forbidden-patterns`
  (gitignored), one extended regex per line. Without that file, `just hygiene` skips the check.
