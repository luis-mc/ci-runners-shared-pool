# RELEASE-73 cloud agent: common setup and rules

- Read AGENTS.md, CLAUDE.md, .claude/rules/*.md, docs/README.md first.
- Setup (~3 min): Node 24 from https://nodejs.org/dist/latest-v24.x/ (linux-x64 tar.xz) into /tmp/node24, its bin first on PATH, `corepack enable --install-directory /tmp/node24/bin`; `export GOTOOLCHAIN=go1.27.4`; `git config core.hooksPath .githooks`; `pnpm install --frozen-lockfile`; gitleaks 8.30.1 linux_x64 into ~/bin (sha256 551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb), ~/bin first on PATH; `GOTOOLCHAIN=go1.27.4 GOBIN=$HOME/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0`; docs scripts: `git clone -q https://github.com/luis-mc/dev-standards /tmp/ds && git -C /tmp/ds checkout -q 7f582c8`; `D=/tmp/ds/claude/plugins/docs/scripts`.
- Branch: `git fetch origin CRSP383-integration CRSP383-work-notes`; `git checkout -b <branch> origin/CRSP383-integration`. Owner notes: `git show origin/CRSP383-work-notes:docs/work/<file>`.
- Report first at docs/work/<task>-report.md; commit and push after every step; keep it current. In your LAST commit `git rm` your docs/work files (copy the report into the PR body): docs/work never reaches the merge.
- Docs: update the feature spec; add a change note `python $D/docs_fold.py note --doc features/<feature> --task <TASK> --change minor --text "..."`; `python $D/docs_index.py build`; `python $D/docs_check.py --strict` must give 0 errors 0 warnings; if docs/design-system/next-version changed also run `python $D/design_system.py visuals next-version --apply`. Never hand-edit generated files.
- UI work: follow docs/design-system (highest v folder, v4) and the AGENTS.md design-system checklist (fill it in the PR body); shared parts from packages/ui only, no app CSS, no style=; every new word in next-version content.md with a CHANGES entry; `pnpm --filter <pkg> test` and citation-test; e2e spec for a new screen (it runs in CI).
- Tests: only what you touch (go test -race on touched packages, vitest/pytest for touched files). No Docker builds.
- Commits: task ID in the message; NO Co-Authored-By, Claude-Session or any AI attribution line (the commit-msg hook refuses them). Never --no-verify, never DELIVERY_FREEZE_OVERRIDE, never touch .gitleaksignore or .gitleaks.toml; fake secrets in tests are built at run time.
- Push `git push -u origin <branch>`; the pre-push hook runs ~3 min; fix what it reports.
- PR: GraphQL is blocked; open a DRAFT PR to CRSP383-integration with `gh api repos/luis-mc/ci-runners-shared-pool/pulls -f title='<TASKS>: ...' -f head=<branch> -f base=CRSP383-integration -F draft=true -f body=@body.md` (no attribution). Never mark ready, never merge, never comment, never push main or CRSP383-integration.
- Owner rule: the CRSP product does all the work itself; every setup and admin step is in the web console (and the CLI); never tell the user to edit files, run terminal steps or change GitHub settings by hand unless impossible.
- Final message: READY or NOT READY, files changed, tests with results, PR number, head sha, open questions for the owner.

## Added 28 Nov 06:20

- CRSP383-integration now contains main (RELEASE-71 to RELEASE-74, merged at f7c78216). Branch from it fresh.
- e2e specs: a cloud sandbox cannot pass the pre-push e2e gate (it wants the full Playwright suite on the exact head, ~3.5 h here, and its result publish is refused). So never put a new or changed spec under `e2e/` on your branch. Write it as `docs/work/e2e/<path-under-e2e>` (same content, e.g. `docs/work/e2e/tests/web-console/foo.spec.ts`), run it locally with Playwright from there if you can (copy it into e2e/ in your sandbox only, run, then remove it before committing), and say so in the PR body. Keep these files (do not `git rm` docs/work/e2e). The orchestrator moves them into `e2e/` at the join and runs the full gate on the joined head before the ready PR.
- Never use pkill/kill by name; stop only the process ids you started.
