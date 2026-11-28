# CRSP378 report (RELEASE-73)

Status: READY (draft PR to CRSP383-integration).

- New `apps/controller/internal/jobsdata/live`: wraps the jobs store. After UpsertJob commits it publishes job.queued / job.started / job.finished once per state change (job payload = list row, with id and run_id). Groups and Stats are served from memory, built at start, rebuilt in the background (coalesced, 2 s gap) after UpsertJob, SetFacts, Prune. Failed rebuild keeps the last good answer.
- `api.JobEventJSON` encodes the payload. Wiring in `cmd/crsp/areas_wiring.go` (hub made before the jobs ingest; the API reads the wrapped store).
- Console: `useSettledVersion` gathers a burst of job events into one reload per 0.5 s; job lists use it. `job.finished` also clears the queued strip.
- Spec: observability R5.78 + change note CRSP378.
- Tests: go test -race (live x10, api, cmd/crsp, jobsdata/...) green; vitest whole console 100 files / 901 tests green; eslint and tsc clean.
