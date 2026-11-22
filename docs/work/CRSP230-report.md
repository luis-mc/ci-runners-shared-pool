# CRSP230 report: LC-11 declared drops

Branch CRSP230-lc11-declared-drops, from origin/OPS77-integration.

- `internal/store/declared_changes.go`: one table, per migration number: tables dropped, tables rewritten (table, column, JSON key), task, reason. Entries 42 (CRSP227, drops access_credentials) and 43 (CRSP229, rewrites repos_state.body, key legacy_vars).
- `store.UserDataVersion` reads the highest applied goose version read-only. The snapshot file is now `{schema_version, tables}`.
- `dbCompare` (`cmd/crsp-lifecycle-test/userdata.go`) applies an entry only when before version < migration <= after version. A dropped table must be absent (emptied fails); a rewritten column is compared with the JSON key removed on both sides, all rows and other cells must match. Accepted changes print as `LC-11 note: declared by migration N (TASK): ...`.
- `scripts/lifecycle/windows.ps1`: captures the tool's stdout and writes each note as its own line in lifecycle-steps.txt. No other LC rule changed; lifecycle_optout.py untouched.
- Tests: undeclared drop fails; undeclared row change fails; declared drop passes only across 42; declared drop leaving the table (kept or emptied) fails; declared rewrite losing a row fails; rewrite outside 43 fails.
- Docs: lifecycle.json `upgrade_data_rule` + CHANGES C36 (next-version only); operations spec R5.125 and R5.85 + change note CRSP230; `docs/guides/upgrade-notes.md` is added to the release notes by release.yml (parity tokens deleted on upgrade).
- CRSP229 had not changed LC-11 or the migrations on OPS77-integration at the last fetch (no 00043 yet); nothing replaced. The 43 entry is ready for when it lands.

CI on 51d14f689f: ci.yml run 45032269776 green (its lifecycle job is skipped off a release PR). lifecycle.yml run 45034935884 (dispatched on the same head) green: both Windows lanes print `LC-11 note: declared by migration 42 (CRSP227): table access_credentials dropped` and `LC-11 PASS`.
Checks: go build, vet (and GOOS=windows), golangci-lint v2.15.0, go test cmd/crsp-lifecycle-test + internal/store, fast_guards, docs_check all clean.

## Round 2

- Merged origin/OPS77-integration (ef660892). Conflicts: `docs/design-system/next-version/CHANGES.md` (both entries kept; mine renumbered C35 -> C36, after their C35); `docs/index.html` regenerated with docs_index.py build, `design_system.py visuals next-version` rerun. 00042_drop_access_credentials.sql and 00043_drop_legacy_vars.sql both exist.
- 43 declaration fixed to match the real migration: `DeclaredRewrite.JSONKey` is now `JSONPaths`; entry 43 removes `$.policies.*.LegacyVars` and `$.policies.*.legacy_vars` (`*` = every policy). `dbCompare` strips exactly those nested keys on both sides (`withoutJSONPaths`/`removePath`); a top-level `legacy_vars` is no longer stripped. Nothing else in LC-11 changed (change note CRSP230 wording updated).
- Test `TestDeclaredRewrite` uses the real 43 entry: policies that lose LegacyVars pass across 43; a lost policy, a changed policy field, a lost top-level key, a lost row, and a rewrite outside 43 fail.
- Checks: go build, vet (and GOOS=windows), golangci-lint v2.15.0, go test cmd/crsp-lifecycle-test + internal/store, fast_guards, docs_check (only the expected W1) clean.
