# Agent delivery details

The rules a project reads in every session are in `.claude/rules/agents-and-integration.md`. This guide holds the rest: read it when you join work, run CI, clean up or brief an agent that needs an outside site. Each line names what enforces it.

## Agents and the CI-gate path
- CI-gate path (only in a project that has `scripts/delivery/agent_ci.py`): an agent does not run the full local gate. It runs the tests of what it touched and the fast guards, pushes, and writes a `CODE DONE` line in its report; `scripts/delivery/agent_ci.py` then starts one CI run on that branch head, and that run is the agent's gate. <!-- check: DL-81, DL-12 -->
- On the CI-gate path a CI run started by hand plans what the branch brings since the default branch (`origin/<default>...HEAD`), never the last commit. A CI plan script that a helper imports has no top-level `await` (it deadlocks). <!-- check: DL-81, DL-83 -->
- Before briefing an agent that needs an external site, run `scripts/delivery/reach_check.py` in its sandbox; if it fails, fetch the material locally and commit it. <!-- check: DL-43 -->
- Worktrees and scratch live under the configured work root, never under the projects folder; remove them when done. <!-- check: DL-53 -->

## Integrate once, deploy once
- Trust an agent branch's gate only via `gate-result.json` matching the current head and base (`scripts/delivery/gate_verify.py`); the join checks it per branch, and no gate result is needed for the joined head or the ready PR. <!-- check: DL-13 -->
- The branch gate runs the always-on guards first, then typecheck + lint of the touched packages and their dependents, then unit / integration / e2e; a time-limited run always covers lint and types, and the pre-push hook runs them too. <!-- check: DL-84 -->
- A join is light: `scripts/delivery/join.py` merges the branches, resolves conflicts, folds spec notes, then checks typecheck and lint of what the merges touched and the fast guards; push and open the ready PR at once. Never run the branch gate or a full suite on the joined head, wait for a sandbox gate before the ready PR, or re-gate after a join fix. <!-- check: DL-11 -->
- On the CI-gate path the orchestrator joins urgent work at code push (a light join, `scripts/delivery/join.py --ci-gate`, no gate result needed) and sizes each ETA from coding time plus the branch CI run, not from a padded local gate. <!-- check: DL-83, DL-30 -->
- CI scope (`scripts/delivery/ci_scope.py`, patterns in `scripts/delivery/ci-scope.json`): a release PR runs the full suite on its first run and later pushes carry the jobs they do not reach (`carry_plan.py`); a change of CI files only runs the CI tests and the named smoke set; root configuration or the lockfile runs the full suite; everything else runs what it affects. Each run reports its time as the artifact `ci-run-times`. <!-- check: DL-80, DL-82 -->

## Leaner CI (RELEASE-73)
- The local gate is a smoke: `gate.json` `local_gate` is `smoke`, and the pre-push hook runs `go test -short` on the touched packages. CI is the one full run; `CRSP_PREPUSH_FULL=1` runs the touched packages in full, and `branch_gate.py --full` runs everything. <!-- check: DL-12, DL-122 -->
- The pre-push hook and the branch gate also run `GOOS=windows golangci-lint` (when Go files changed) and `pnpm -r lint` (when web files changed), the two lints CI runs, so they stop here and not in the ready PR's run. <!-- check: DL-84 -->
- `.delivery/config.json` sets `watcher.merge_result: update` (the watcher merges the default branch into a ready PR whose head is behind, and merges only after CI is green on the new head) and `watcher.infra_retry: true` (one re-run of a job that failed before any project step ran; never a test failure). `ci.yml` decides the push to main with `scripts/delivery/ci_skip_check.py`; a config-only pull request skips through `scripts/ci/pr_config_only.py`. <!-- check: DL-123, DL-99 -->
- Pass records (`scripts/delivery/ci-shards.json`, `pass_records.py`): the image, `test-os` and `msi` lanes list their `inputs`. `lane-records` in `ci.yml` and `records` in `lifecycle.yml` read only the default branch's records and skip a lane whose key has a record; the `msi` skip writes a record-backed lane result that `lifecycle_release_check.py` accepts only when the record is the default branch's and the key matches now (DL-112). The `pass-records` job writes the records of lanes that ran and passed, and copies a merged PR's records to main when its push skipped. A lane without inputs, a missing key or a manual run always runs. Keep the inputs wide: a missing path loses a run, a skip that should not happen loses proof. <!-- check: DL-118, DL-112 -->
- Flakes: `.delivery/quarantine.json` lists `{test, task, review, reason}`. `scripts/ci/go-test.sh` leaves the listed Go tests out of the blocking run (`go test -skip`) and `GO_TEST_QUARANTINE=only` runs just them in a non-blocking CI step. `quarantine_check.py check` fails an entry with no task or a past review date (lint job and the gate's always-on guard). No retry-until-green. <!-- check: DL-121, DL-99 -->
- E2E shard weights (`scripts/ci/e2e-weights.json`) are refreshed from the per-test times of a green full run (`ci-e2e-times-*` artifacts) with `shard_balance.py update`; the file keeps `routes_wall_seconds` and the `e2e/tests/...` path keys, so check the diff before committing. The artifact host is not reachable from agent sandboxes: do it where artifacts download.

## Watching CI and its guardrails
- Never `gh run watch` without `--interval 60` or more: the default 3 s drains the GitHub quota every session shares. Watch CI with `ci_watch.py`, one watcher for many runs (`ci_watch.py SHA1 SHA2 --run ID`). <!-- check: DL-95 -->
- A red release CI run finishes: never cancel it while jobs still run; wait, fix every failure, push once. Only a failure that blocks all later jobs (setup, lockfile, build) may be cancelled, with `# D105: blocks-all`. <!-- check: DL-97 -->
- No fail-fast on release PR CI: every matrix sets `fail-fast: false` and no suite stops at its first failure (`ci.jobs.<id>.fail_fast`). The sandbox branch gate keeps its cheapest-first stop. <!-- check: DL-98 -->
- Tests keep guardrails G1-G7: per-test timeout (unit 10 s, integration 60 s, e2e 2 min), job timeout-minutes ≤ 30, no CI retries, no sleeps, ≤ 20 cases per test, mutation off PR CI, test times reported. <!-- check: DL-99 -->

## Releases, deploys and cleanup
- A release leaves when its tasks are green; late branches ride the next release. <!-- check: DL-01, DL-16 -->
- No preview deploys for agent branches. Never merge mid-round to "see it deployed". <!-- check: DL-19 -->
- Never `vercel env rm` to narrow targets; after an env change re-list Production. <!-- check: DL-20 -->
- A gate that fails at `setup` means a stale lockfile (regenerate and commit it) or an install killed for memory (exit 137: larger pool cap or fewer parallel jobs). Never install unfrozen. <!-- check: DL-23 -->
- After the ready PR merges, run `scripts/delivery/cleanup.py --pr <N>`: it removes the release's branches, gate-result refs and worktrees. Leave nothing behind. <!-- check: DL-14 -->
- Build artifacts on GitHub (installers such as MSI, bundles, reports): keep only the newest of each name. Give each kind a stable name; `artifact-prune.yml` deletes the older ones once a day. <!-- check: DL-22 -->
- CI jobs fetch pinned tools through `scripts/delivery/ci_tool.py`: a per-runner cache, sha256-checked, with retries; never a bare download into RUNNER_TEMP. <!-- check: DL-94 -->
