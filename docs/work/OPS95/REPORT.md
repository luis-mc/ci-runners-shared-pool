# OPS95 report

Status: BLOCKED, no code changed. No PR opened.

OPS94's report (origin/OPS94-pool-agent-fixes) reached the same result. Checked again on origin/main:

- No `crsp pool-refresh` and no `pool-add` command, script or test in this repo (`git grep`: only docs and registry.json).
- `docs/features/dependency-setup/spec.md` line 58 says: `pool-refresh` is not a crsp command; it is the dev-standards script. The failure in registry OPS95 ("could not compare the image ids", fixed by hand with `compose up -d`) comes from that script.
- Nearest in-repo code: `Backend.Refresh` in `internal/backend/container/refresh.go`. When the pool container is absent it returns "no pool container yet; the next runner starts on image X" on purpose (the next `Start` creates it). That is a design choice, not the reported bug. It has no "create every missing container / report created" contract, and there is no pool-add flag set to copy.

Not done, because it would be a guess: changing `Refresh` to create the container eagerly.

Needed from owner, one of:
1. Attach the dev-standards repo (name) so the real script and its fake-runtime tests can be fixed; or
2. Confirm that `Backend.Refresh` should create a missing pool container itself (then: create via `RunArgs` as `swap` does, set `RefreshResult.Created`, test with the fake engine in `fake_test.go`).
