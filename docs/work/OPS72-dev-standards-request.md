# Draft for the dev-standards maintainers (OPS72) — NOT SENT

Draft only. Owner: edit it, then send it yourself. Nothing was posted to dev-standards.

**Subject:** luismc-delivery: please make crsp's local edits config options or hooks

Hi,

crsp (`luis-mc/ci-runners-shared-pool`, track `go`) just synced to luismc-delivery 1.54.1. The installer kept 10 files
("kept (project changes)") because crsp edits them, and for 4 more crsp took the template and re-set a few values by hand.
The scripts themselves (every `scripts/delivery/*.py`) need no edit. Each crsp edit below could be a config option or a hook,
so a future `update` applies cleanly. Names are proposals; `.delivery/config.json` is the home for all of them.

| # | File | What crsp changed | Why | Proposed option and default |
| --- | --- | --- | --- | --- |
| 1 | `.githooks/commit-msg` | Enforces `no_attribution` also when `.delivery/config.json` is missing (the file is git-ignored and absent in cloud clones). | The no-attribution rule lives in CLAUDE.md; a missing config must not turn it off. | `hooks.commit_msg.enforce_without_config` (default `false`; crsp `true`). |
| 2 | `.githooks/pre-push` | Appends `project_gates`: gofmt, `go vet`, golangci-lint, `go test` of changed packages, web typecheck. | Fast local gates the guards do not cover. | `hooks.pre_push.extra: ["<command>", ...]` run after the guards, or a `scripts/delivery/pre-push.d/*.sh` hook folder (default none). |
| 3 | `.github/workflows/artifact-prune.yml` | Runs the project's `scripts/ci/artifact-prune.sh` instead of `artifact_prune.py`; newer checkout pin. | Older project script predates `artifact_prune.py`. | `artifact_prune.command` (default `python3 scripts/delivery/artifact_prune.py`); `ci.action_pins` (see 8). |
| 4 | `.github/workflows/ci-fallback.yml` | Adds Go, pnpm, node and frozen-install steps before the claim (the template has a `PROJECT STEP` comment). | The shard needs the project's toolchain. | `ci_fallback.setup_steps: [<yaml step>, ...]` rendered at the marked place (default empty). |
| 5 | `.github/workflows/package-windows.yml` | OPS69 asset names via `scripts/ci/release-names.sh`; no `v*` tag trigger; skips build until `installer/` exists. | `release.yml` builds, signs and publishes the MSI on tags; the template's build step is a placeholder. | `release_assets.name_command` (default none, uses `versioned_names`); `package_windows.on_tags` (default `true`; crsp `false`); `package_windows.build_if_path_exists` (default none; crsp `installer`). |
| 6 | `.github/workflows/windows-msi-check.yml` | Skips steps until `windows-build-tools.json` exists; adds an authoring check of `installer/crsp.wxs` (one programs-list entry, one UpgradeCode, major upgrade). | The MSI is wired later; the check guards the authoring. | `windows_msi_check.enabled_if_file` (default none); `windows_msi_check.extra_steps` (default empty). |
| 7 | `scripts/delivery/smoke_packaged.py` | `probe_context()`: a loopback `https` health URL is probed without certificate verification. | The controller serves HTTPS with its own CA on non-loopback addresses; the smoke run does not trust it. | `smoke.insecure_loopback_https` (default `false`; crsp `true`), read from `smoke.json`. |
| 8 | `.github/workflows/train-cleanup.yml`, `package-windows.yml`, `windows-msi-check.yml`, `artifact-prune.yml` | Newer action pins (checkout v7.0.1, setup-python v7.1.0, cache v6.1.0) and comment alignment. | Project bumps pins by its own schedule (RELEASE-18). | `ci.action_pins: {"actions/checkout": "<sha> # v7.0.1", ...}` applied by the installer to every workflow (default: the template's pins). |
| 9 | `scripts/delivery/tools.json` | Adds `syft` 1.33.0 and `nfpm` 2.49.0 with sha256. | Release SBOM and Linux packages. | `ci-tools.local.json` merged into `tools.json` by `ci_tool.py` (default absent). |
| 10 | `scripts/delivery/windows-msi-linux.sh` | One comment (OPS69 names instead of DL-96). | Follows edit 5. | Goes away with edit 5; no option needed. |
| 11 | `.github/workflows/package.yml` | Build line `-o dist/crsp ./cmd/crsp` instead of `dist/app ./cmd/...`. | Names the product. | `package.build_command` (default the template's line); today a "PRODUCT:" hand edit. |
| 12 | `scripts/delivery/smoke.json` | `build`, `start`, `expect`, `health` filled. | Per-repo values. | Already a documented fill-in; please keep `smoke.json` outside the "template differs" report once `start` is set. |
| 13 | `scripts/delivery/gate.json` | Before this sync: `-timeout 10s` on the guards step, `bash scripts/ci/go-test.sh -short` as unit step, an `e2e` step for `ui-changed`. Now: template plus `ui_paths` and `-timeout 10s` only. | G1 wants a per-test timeout; the project runs tiered Go tests and Playwright. | Template: `-timeout 10s` on the guards step by default (so G1 passes out of the box); `gate.extra_steps: [{name, scope, run, run_full, env}]` and `gate.step_overrides: {unit: "<cmd>"}` merged by the installer (default none). |
| 14 | `.gitleaks.toml` | Before this sync: three allowlist regexes for fake example values (a test signing key, `whsec-...-SIGNING` test secrets, a form length constant). | The history scan reads them as secrets. | A project allowlist file (for example `.gitleaks.project.toml`, project-owned, never overwritten) that the template `.gitleaks.toml` includes, so a project keeps its own fake-value regexes without editing the shipped file. Default: no file. Until then crsp keeps its three regexes inside `.gitleaks.toml` (owner decision, 22 Nov, OPS71). |
| 15 | `.github/workflows/ci.yml` | Project-owned 700-line workflow. | Needs jobs the go template does not have (e2e shards, Windows and macOS tests, release targets). | `ci.extra_jobs_file` (default none), or document that a project may own `ci.yml` and list it by default in `ci-install.json` `keep`. |

Also useful, small:

- `update` could write a one-line version stamp in `.delivery/ci-install-state.json` that is also committed, since `.delivery/` is
  git-ignored in crsp and a fresh clone cannot tell which delivery version was installed (`delivery.installed_version`).
- When `update` has no install record it keeps every differing file; a `--baseline <version>` option would let it 3-way merge
  against the template of that version.

Thanks.
