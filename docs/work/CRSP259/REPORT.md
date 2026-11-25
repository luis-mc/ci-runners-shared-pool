# CRSP259 report: config as code

Branch CRSP259-config-as-code (this branch keeps the report; the PR branch CRSP259-integration does not).

## Done
- `internal/configio`: engine (export, plan, apply, stale refusal, removal yes, audit per change, secret-value refusal) and sections for settings, pool, queue, runner classes, machines, image layers, repository policies, notification channels. Tests R5.28 to R5.30, R6.10.
- API: GET /api/v1/config/export (admin), POST /api/v1/config/plans (admin), POST /api/v1/config/plans/{id}/apply (owner). OpenAPI, Go and TS codegen. API tests.
- Console: Settings > Data > Config as code (Export, wizard File, Review, Applying, Done). Vitest.
- Operations spec and change note CRSP259.

## Not done / flagged
- `crsp config` CLI commands (spec R5.28 to R5.30 name them): not built.
- Playwright spec for R5.31: not written. Section adapters (machines, repositories, notifications, runner classes, image layers) have no unit test of their own; only settings, pool, queue run through the API test.
- No done-after-reload (apply is one request, no stored outcome) and no Stop.
- github-integration R5.7 is key rotation, already built (CRSP115); not config as code. Untouched.
- docs_check.py / docs_fold.py: luismc-docs plugin not in this sandbox; change note written by hand in the same format. Run docs_check in CI/locally.
- Local tooling: installed gitleaks 8.28 and golangci-lint 2.15 into a scratch dir so the pre-push hook ran for real. Node is 22 here (repo wants 24): ConfigExport and AuditLog vitest export tests fail on main too in this env.
- macOS: compiled and unit-tested in CI only, not run on a real Mac (said in the spec).
