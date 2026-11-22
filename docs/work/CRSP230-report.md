# CRSP230 report: LC-11 declared drops

Branch CRSP230-lc11-declared-drops, from origin/OPS77-integration.

- `internal/store/declared_changes.go`: one table, per migration number: tables dropped, tables rewritten (table, column, JSON key), task, reason. Entries 42 (CRSP227, drops access_credentials) and 43 (CRSP229, rewrites repos_state.body, key legacy_vars).
- `store.UserDataVersion` reads the highest applied goose version read-only. The snapshot file is now `{schema_version, tables}`.
- `dbCompare` (`cmd/crsp-lifecycle-test/userdata.go`) applies an entry only when before version < migration <= after version. A dropped table must be absent (emptied fails); a rewritten column is compared with the JSON key removed on both sides, all rows and other cells must match. Accepted changes print as `LC-11 note: declared by migration N (TASK): ...`.
- `scripts/lifecycle/windows.ps1`: captures the tool's stdout and writes each note as its own line in lifecycle-steps.txt. No other LC rule changed; lifecycle_optout.py untouched.
- Tests: undeclared drop fails; undeclared row change fails; declared drop passes only across 42; declared drop leaving the table (kept or emptied) fails; declared rewrite losing a row fails; rewrite outside 43 fails.
- Docs: lifecycle.json `upgrade_data_rule` + CHANGES C35 (next-version only); operations spec R5.125 and R5.85 + change note CRSP230; `docs/guides/upgrade-notes.md` is added to the release notes by release.yml (parity tokens deleted on upgrade).
- CRSP229 had not changed LC-11 or the migrations on OPS77-integration at the last fetch (no 00043 yet); nothing replaced. The 43 entry is ready for when it lands.
