#!/usr/bin/env bash
# Fails when tracked files or commit metadata (author, committer, message)
# match a forbidden pattern.
#
# The patterns are never stored in the repository. They come from the
# FORBIDDEN_PATTERNS environment variable (a repository secret in CI, so it is
# masked in logs) or from the gitignored .forbidden-patterns file. Patterns are
# extended regexes matched case-insensitively, one per line or joined with "|".
#
# Findings are reported as file:line or commit hash only, so the matched text
# (and therefore the pattern list) never appears in logs.
set -euo pipefail

patterns="${FORBIDDEN_PATTERNS:-}"
if [[ -z "$patterns" && -f .forbidden-patterns ]]; then
  patterns="$(cat .forbidden-patterns)"
fi

if [[ -z "$patterns" ]]; then
  if [[ -n "${CI:-}" ]]; then
    echo "error: FORBIDDEN_PATTERNS is not configured" >&2
    exit 1
  fi
  echo "skipping forbidden-strings check: no patterns configured (see CONTRIBUTING.md)" >&2
  exit 0
fi

pattern_file="$(mktemp)"
trap 'rm -f "$pattern_file"' EXIT
printf '%s\n' "$patterns" | sed '/^[[:space:]]*$/d' >"$pattern_file"

failed=0

rc=0
matches="$(git grep -I -n -i -E -f "$pattern_file" | cut -d: -f1,2)" || rc=$?
case $rc in
  0)
    echo "error: forbidden pattern found in tracked files:" >&2
    sed 's/^/  /' <<<"$matches" >&2
    failed=1
    ;;
  1) ;;
  *) echo "error: scanning tracked files failed (exit $rc)" >&2; exit "$rc" ;;
esac

if git rev-parse --verify --quiet HEAD >/dev/null; then
  while read -r commit; do
    rc=0
    git show -s --format='%an <%ae>%n%cn <%ce>%n%B' "$commit" | grep -q -i -E -f "$pattern_file" || rc=$?
    case $rc in
      0) echo "error: forbidden pattern found in metadata of commit $commit" >&2; failed=1 ;;
      1) ;;
      *) echo "error: scanning commit $commit failed (exit $rc)" >&2; exit "$rc" ;;
    esac
  done < <(git rev-list HEAD)
fi

if [[ $failed -eq 0 ]]; then
  echo "forbidden-strings check passed"
fi
exit "$failed"
