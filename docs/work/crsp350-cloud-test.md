# CRSP350 cloud capability test

| # | Step | Result |
|---|------|--------|
| 1 | fetch CRSP334-integration, branch CRSP350-cloud-test | works |
| 2 | luismc-docs plugin scripts | NOT found locally (`find / -name docs_check.py` empty; ~/.claude/plugins has no docs plugin). Clone of github.com/luis-mc/dev-standards works; `docs_check.py --strict` runs: 1 error `G1 docs/architecture/decisions.md: generated file is stale: run docs_index.py build` (+ info: shallow clone, T2 skipped, from my own --depth 1 clone of dev-standards context). `project_artifacts.py --help` works |
| 3 | gitleaks | fails: `gitleaks: command not found`. `scripts/delivery/fast_guards.py` runs rc=0, G4 reports 2 breaks (sleeps in agentlink/deps_test.go:54, updatewindow/web_test.go:110) |
| 4 | commit + push without --no-verify | commit + push work. Hooks did NOT run: `core.hooksPath` unset in the fresh clone (set by the package.json "prepare" script, not run) |
| 5 | `gh pr create --draft` | fails: `HTTP 403: GitHub GraphQL is not available from Claude Code sessions`. `gh api repos/.../pulls -F draft=true` works: PR #490 |
| 6 | write report, push | works |
