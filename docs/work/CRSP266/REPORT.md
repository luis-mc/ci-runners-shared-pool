# CRSP266 report (plan row S, gap G4: debug access to a running job)

Task: write the SPEC only. No code. Owns docs/features.

## Status
- [x] Branch CRSP266-debug-access pushed from origin/main.
- [x] Facts read: AGENTS.md, .claude/rules, docs/README.md, study G4, runners R5.25-R5.33 + R5.81/R5.82, machines R5.3a, web-console R5.13, decision 0004, notifications spec (format model).
- [x] Spec written: docs/features/debug-access/spec.md (new feature folder).
- [x] Change note docs/features/debug-access/changes/CRSP266.md (by hand).
- [ ] docs_check.py run.
- [ ] Rename to CRSP266-integration, push, open ONE ready PR.

## Notes
- The luismc-docs plugin (docs_fold.py, docs_check.py) is not installed in this cloud session (`find / -name docs_check.py` found nothing; plugins list is empty). The change note is written by hand in the exact format of the existing notes; docs_check.py could not be run. Owner: run it once on the PC.

## Done
- Spec: docs/features/debug-access/spec.md, R5.1-R5.34, R6.1-R6.9, Mermaid flow (sections 3, 7), test table (all planned), 9 open questions (section 8).
- Not done: docs_check.py (plugin missing here). Generated indexes (features/index.md etc.) not touched; the docs build will list the new feature.
- Follow-ups at build time: pointers in web-console and runners specs; new notification events in the notifications spec; terminal component in design-system next-version.
- This report is removed from the PR's final tree (docs/work stays on the branch history only).
