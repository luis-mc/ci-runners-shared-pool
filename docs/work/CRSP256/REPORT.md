# CRSP256 report: minutes saved and money saved (plan row F)

Branch `CRSP256-minutes-saved`. Run 2026-11-25 (clock of the session, UTC).

## Built, on screen
- Overview: tiles *Minutes saved today* and *Money saved today* (estimate, rate date). `shots/overview-1300.png`
- Analytics > Savings: Today, 7 days, 30 days, A range; by repository, system, machine; public-repository note. `shots/savings-today-1300.png`, `shots/savings-today-393.png`
- Analytics > Compare (R5.34): crsp against hosted. `shots/compare-1300.png`
- Settings > Repositories > Savings rates: editable rates, size per machine, source URL and date, reset. `shots/rates-settings-1300.png`
- Backend: `internal/history` (rates, report), `internal/store/savings.go`, routes `GET /api/v1/savings`, `GET/PUT/DELETE /api/v1/savings/rates` (owner-only writes, audited in the same transaction). The live Overview feed was an empty stub; it now counts real finished pool jobs.
- Screenshots are of the built console in Chromium with a mocked API (fixture data, not a live controller).

## Not done or not as asked (read these)
1. **Rates not re-copied from GitHub.** docs.github.com is blocked in this session. Shipped rates are the existing `internal/compare/prices.json` values, stamped 2026-11-15 (the file's commit date). Someone must re-read the page and move `ShippedCopiedOn`.
2. **G6 (fallback reason next to each fallback run) not built.** No per-run reason is stored; routing `why` is per repository, now. Showing it beside old runs would mislead. Needs the reason saved on the job row. Named in spec section 11.
3. No CLI command, no config-file route for rates.
4. `docs_fold.py` and `docs_check.py` (luismc-docs plugin) are not in this session. Change notes written by hand in the same format (`docs/features/{observability,web-console}/changes/CRSP256.md`); docs_check NOT run.
5. Windows not run; Linux only. macOS compiled and unit-tested in CI only (stated in the spec).
6. Design-system checklist is in the PR body. The step-by-step checks were not all run: wizard items do not apply (no wizard); the enforcement kit `scripts/design` and `guards` tests pass (133 tests); DOM layout at 360/393/1300 was not run beyond the screenshots.

## Tests run locally
Go: `internal/history`, `internal/store` (savings), `internal/api`, `internal/cli`, `cmd/crsp` pass. Web: savings, overview, settings, analytics pass except 2 download tests (AuditLog, ConfigExport: Blob text) that fail in this Node 22 sandbox and touch no changed code; CI on Node 24 decides. eslint, tsc, prettier clean. Pre-push hooks needed local tools (gitleaks, golangci-lint, Node engine override) installed in the sandbox.
