# OPS71 report: delivery sync to luismc-delivery 1.54.1

- Source: luis-mc/dev-standards, commit `28a7dba5` (RELEASE-162, plugin `delivery` 1.54.1; checked out at `0333478a`).
- Track: `go` (`scripts/delivery/gate.json` says `"track": "go"`). crsp has no `.delivery/config.json` or install record
  (`.delivery/` is in `.gitignore`), so no installed version is recorded; I compared every file with the 1.54.1 template
  and, where a file was an older template, the difference is only crsp edits (see table).
- `delivery_ci_install.py update --track go --root .` wrote nothing over the project (no install record: every differing file kept).
  I then merged by hand. The `.new` files were deleted.
- All 55 other managed files, including `e2e_push_check`, `delivery_common`, `lifecycle_release_check`, `lifecycle_optout`
  and `lifecycle_conformance`, are byte-identical to 1.54.1 ("same"). Nothing to do for them.
- `.gitleaksignore` untouched.

## Vendored files that differ from the 1.54.1 template

| File | Result | Note |
| --- | --- | --- |
| `.gitleaks.toml` | template + main's 3 regexes | Owner decision, 22 Nov: the 1.54.1 template plus the three allowlist regexes main already has, copied verbatim with their comments (a fake signing key of the contract history, CRSP32; the fake `whsec` signing secrets of the notification tests, and the form's length-limit constant, CRSP37). Nothing else added; main's scan behaviour is unchanged. |
| `.github/workflows/package.yml` | template + per-repo value | Build line set to `-o dist/crsp ./cmd/crsp` (PRODUCT value, matches `smoke.json`). Checkout and setup-python pins go back to the template's `v4`/`v5` pins. |
| `scripts/delivery/gate.json` | template + per-repo value | `ui_paths: ["apps/web/", "packages/ui/"]`. Keeps `-timeout 10s` on the guards step (not weakening G1). Drops `bash scripts/ci/go-test.sh -short` as unit step, and the `e2e` step (see "Risks"). |
| `scripts/delivery/smoke.json` | template + per-repo values | `build`, `start`, `expect`, `health` restored (the template says "Fill start..."). Only `_note` text and `example_start` differ from before. |
| `.github/workflows/artifact-prune.yml` | template + crsp edit | New header comments and the "Check the workflow list" step from the template. Edit kept: runs `scripts/ci/artifact-prune.sh` with `GH_REPO` instead of `artifact_prune.py`; the workflow list note is crsp's 8 workflows; checkout pin stays `v7.0.1`. |
| `.githooks/commit-msg` | kept (= template + crsp edit) | Missing `.delivery/config.json` still enforces the no-attribution rule. Template has no newer content. |
| `.githooks/pre-push` | kept (= template + crsp edit) | Appends `project_gates` (gofmt, go vet, golangci-lint, go test of changed packages, web typecheck). No newer template content. |
| `.github/workflows/ci-fallback.yml` | kept (= template + crsp edit) | Adds the Go, pnpm, node and frozen-install setup before the claim; prettier quote style. |
| `.github/workflows/package-windows.yml` | kept (= template + crsp edit) | OPS69 release names via `scripts/ci/release-names.sh`; no tag trigger (release.yml publishes the MSI, CRSP145); installer gate; newer checkout pin. Template's DL-96 `versioned_names` option is not used. |
| `.github/workflows/train-cleanup.yml` | kept (= template + crsp edit) | Newer action pins (`checkout v7.0.1`, `setup-python v7.1.0`), comment alignment. |
| `.github/workflows/windows-msi-check.yml` | kept (= template + crsp edit) | Gate steps until the MSI is wired; `actions/cache` pin; programs-list authoring check for `installer/crsp.wxs`. |
| `scripts/delivery/smoke_packaged.py` | kept (= template + crsp edit) | `probe_context`: a loopback https URL is probed without certificate verification. |
| `scripts/delivery/tools.json` | kept (= template + crsp edit) | Adds `syft` 1.33.0 and `nfpm` 2.49.0 (release SBOM and packages). |
| `scripts/delivery/windows-msi-linux.sh` | kept (= template + crsp edit) | Comment only (OPS69 names instead of DL-96). |
| `.github/workflows/ci.yml` | kept | Project-owned (the template says the project edits it); crsp's 712-line workflow shares no structure with the 254-line template. Not touched. |

That is 15 files that differ; 12 carry a crsp edit once pin-only and format-only differences are set aside
(`.gitleaks.toml`, `gate.json`, `smoke.json`, `package.yml`, `artifact-prune.yml`, `commit-msg`, `pre-push`, `ci-fallback.yml`,
`package-windows.yml`, `windows-msi-check.yml`, `smoke_packaged.py`, `tools.json`), plus `ci.yml`, `train-cleanup.yml` and
`windows-msi-linux.sh` as pin/comment/own-workflow differences.

## Risks from taking the four template files as-is (owner decision)

- `gate.json` no longer has the `e2e` step, so the pre-push hook no longer asks for the e2e gate, and the `unit` step is the template's `go test -short ./...` (G1 warns on it, as it did on crsp's 150 s script).
  CI (`ci.yml`) still runs e2e and tests on its own. The guardrail check below says whether G1 still passes.
- `.gitleaks.toml` first had no allowlist for the three fake values (history scan red). Settled by the owner on 22 Nov: the three main regexes are back (see table).
- `package.yml` uses the template's older action pins.

## Checks

| Check | Result |
| --- | --- |
| `pytest scripts/delivery scripts/ci` | 42 passed, 1 error: `test_guardrails.py::test_limits` is a helper named `test_*` that pytest takes for a test (missing fixture). The file is byte-identical to 1.54.1 and to origin/main; not caused by this branch. |
| `actionlint` | clean |
| `fast_guards.py --base-ref auto` | all steps PASS; G1 warns (mode warn): `conftest.py`, `ci.yml:311`, `scripts/ci/go-test.sh:40` (150 s), `gate.json:53` (unit step). Same class of warn as before the sync. |
| delivery doctor (`delivery_doctor.py self-check`) | all ok except `gh` (not signed in in this sandbox) and a warn that `smoke_packaged.py`, `tools.json`, `windows-msi-linux.sh` differ from the template (crsp edits, table above). |
| `docs_check.py` | one error, W1: `docs/work/` files on the branch. By rule (D9) they never reach main; strip with `docs_work.py strip` before the merge. Docs: none, delivery tooling only, no product behaviour. |
| `gitleaks dir .` and `gitleaks git` (full history, `--config .gitleaks.toml` = template + main's 3 regexes) | 0 findings in both. With the bare template the history scan finds 4 (commit `07cb8b6c` of this branch, where an earlier draft of this report quoted the fake values, and three lines of commit `3016aa75`); the 3 regexes cover all of them. |

## Secret scan: decided

Owner decision, 22 Nov (about 08:00 Lisbon): keep main's three allowlist lines. `.gitleaksignore` is untouched and history is not
rewritten. Commit `07cb8b6c` of this branch stays in the branch history but will not reach main: RELEASE-54 ships as one commit.
This report describes the fake values and does not quote them.
