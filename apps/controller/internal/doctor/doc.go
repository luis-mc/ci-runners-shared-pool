// Package doctor runs health checks over the pool and keeps their findings
// (doctor R5.1 to R5.8, R5.28, R6.1 to R6.8).
//
// # Writing a check
//
// Other areas add their checks through the small Check interface and
// Registry.Register; the engine does the rest:
//
//	reg.Register(doctor.NewCheck("cache.mirror", doctor.GroupController, 7, false,
//	    func(ctx context.Context, env doctor.Env) []doctor.Finding { ... }))
//
// Rules every check keeps:
//
//   - It returns one Finding per subject it looked at, "ok" ones included, so
//     a later problem shows as a change (R5.1).
//   - A finding that is not "ok" carries a Fix: what to do, and where one
//     exists a CLI command and a console link (R5.4).
//   - A check that cannot run returns "skip" with the reason, never "ok" (R5.5).
//   - It never changes state: no write to GitHub, a machine or the database
//     (R5.7). Env gives read-only sources.
//   - It honours ctx: the engine gives each check a deadline (default 10 s,
//     ceiling 60 s, R6.1). A check that outlives it is reported as "skip".
//   - A panic is caught and shown as "fail: check crashed"; other checks still
//     run (R5.6).
//
// A check for a part that is not built yet is not registered, so it does not
// appear in a run (R5.28). Milestone numbers say which milestone added it.
//
// Ported from dev-standards claude/plugins/delivery: scripts/delivery_doctor.py
// (machine_items, service_rows, workflow_items), the runner_health.py template
// (parse_runs_on, resolve_labels, runner_carries, is_hosted) and their tests;
// each check file names its source.
package doctor
