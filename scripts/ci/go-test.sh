#!/usr/bin/env bash
# Runs `go test ./...` and fails when no test ran (STACKSPEC 11.3: a suite that
# finds zero tests must fail, not pass). Extra arguments go to `go test`.
#
# CRSP201: runs `go test -json`, not `-v` (about 122k log lines). The JSON goes
# to $GO_TEST_JSON (default test-results/go-test.json; CI uploads it as the
# artifact ci-go-test-times-<os>). The log gets one line per package, the output
# of failed tests, the package times and the 20 slowest tests. The exit code is
# go test's own; a run with no passing test exits 1.
#
# CRSP209 (G1): `go test -timeout` limits one package binary, not one test, so the
# packages run in three tiers (scripts/ci/go-tiers.json): unit 10 s (every package
# not listed), integration 60 s, and chunked (OPS82): the packages over 60 s run in
# chunks of their tests, each with the 60 s limit. All runs share one JSON file and one summary.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
json="${GO_TEST_JSON:-test-results/go-test.json}"
# CRSP313: GO_TEST_SHARD=K/N runs one share of the packages and chunks (the tests job runs N
# shards in parallel); unset, it runs everything (Windows, macOS, the local gate).
tiers() { go list ./... | node scripts/ci/go-tiers.mjs "$1" ${GO_TEST_SHARD:+"$GO_TEST_SHARD"}; }
unit_pkgs=$(tiers unit)
integration_pkgs=$(tiers integration)
chunked_pkgs=$(tiers chunks)
rc_file=$(mktemp)
trap 'rm -f "$rc_file"' EXIT
set +e
{
  if [ -n "$unit_pkgs" ]; then
    # shellcheck disable=SC2086
    go test -json -timeout 10s "$@" $unit_pkgs || echo "$?" >> "$rc_file"
  fi
  if [ -n "$integration_pkgs" ]; then
    # shellcheck disable=SC2086
    go test -json -timeout 60s "$@" $integration_pkgs || echo "$?" >> "$rc_file"
  fi
  # OPS82 (G1): the timeout flag limits one package binary, so a package over 60 s runs in chunks:
  # each chunk runs a share of its tests, with the integration limit of 60 s.
  # The chunk count per package is in go-tiers.json; the packages run one at a time (-p 1).
  while read -r pkg count; do
    [ -n "$pkg" ] || continue
    names=$(go test -timeout 10s -list '.' "$pkg" | grep -E '^(Test|Example|Fuzz)' || true)
    for ((k = 0; k < count; k++)); do
      integration_run=$(printf '%s\n' "$names" | awk -v n="$count" -v k="$k" 'NF && (NR - 1) % n == k { a = a (a ? "|" : "") $0 } END { if (a) print "^(" a ")$" }')
      [ -n "$integration_run" ] || continue
      go test -json -timeout 60s -p 1 "$@" -run "$integration_run" "$pkg" || echo "$?" >> "$rc_file"
    done
  done <<< "$chunked_pkgs"
} | node scripts/ci/go-test-summary.mjs "$json"
sum_rc=${PIPESTATUS[1]}
set -e
go_rc=0
while read -r r; do
  [ "$r" -eq 0 ] || go_rc=$r
done < "$rc_file"
if [ "$go_rc" -ne 0 ]; then
  echo "Go tests failed with exit code $go_rc" >&2
  exit "$go_rc"
fi
exit "$sum_rc" # 0 passed, 3 zero-test guard, 4 or other: the summary itself broke
