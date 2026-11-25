# CRSP288 report

Done
- P-7: `overview.Service.Get` runs the four reads with errgroup (limit 4). New optional `overview.QueuePager` (`QueuedJobsPage(ctx, limit)`): source caps with LIMIT and counts with a count query; without it, old path (`QueuedJobs`, cap in Go). Production source is `EmptySource` only in this repo, so no SQL adapter exists to change; the pager is the contract the adapter must implement.
- Timeline: machines list, finished jobs, open jobs read concurrently (errgroup, limit 3).
- fillFollowUps: N+1 `GetRunFollowUp` per workflow_run row replaced by one batched query (`parentRunsOf`); plan uses the primary key (test `TestParentRunsQueryUsesPrimaryKey`, EXPLAIN QUERY PLAN).
- go.mod: golang.org/x/sync now a direct dependency.

Reviewed, no change
- insights_jobs.go loops are write paths in transactions (reconcile, background), not request path.
- forecaster already memoizes samples per request.
- handlers_insights, machines, runners: no store call inside a row loop found.

Not done / needs other owners
- `jobsdata_read.go` (CRSP286 owns): `ListRuns` still calls `fillFollowUps` plus `followUpsOf` as two queries per page; could merge.
- No benchmark numbers for 10k runs / 100k jobs: no data set here; CI only.
- Docs: `docs_fold.py` (`<luismc-docs>`) not present in this sandbox, so change note and `docs_check.py` not run. Add: `docs_fold.py note --doc features/observability --task CRSP288 --change minor --text "Overview and timeline read concurrently; follow-up parents batched."`
