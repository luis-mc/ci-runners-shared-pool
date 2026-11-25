# CRSP286 report (perf: console run and job lists, audit P-1 to P-4)

Status: code done, local smoke green (internal/store, internal/api, internal/jobsdata). CI on branch pending.

Done
- P-1 ListRuns: count(*) over the filter, then page by the sort key (expression index runs_sort_key, plan: SCAN USING INDEX, no temp b-tree), then one grouped join query for the page rows (jobs_run_state). State filter is EXISTS on jobs, not 4 counts per run. Output unchanged (existing tests green).
- P-2 ListJobs: explicit jobListCols (only columns jobFromRow reads) on the read pool.
- P-3 migration 00046: runs_sort_key, runs_conclusion, runs_repository, runs_branch, jobs_run_state. EXPLAIN QUERY PLAN asserted in TestListQueryPlans.
- P-4 Store.rdb: query_only pool, 8 conns; write pool stays at 4 (other code reads through it inside transactions, so 1 would risk a deadlock). WAL stays on.
- Test TestListPagesP95 (10k runs, 100k jobs, p95 < 200 ms, 7 list shapes) and benchmarks BenchmarkListRunsPage / BenchmarkListJobsPage. Local: runs page ~18 ms, jobs page ~1.3 ms (was ~200 ms for runs page at this size).
- Change note docs/features/web-console/changes/CRSP286.md written by hand: docs_fold.py / docs_check.py are not in this sandbox; CI dev-standards runs docs_check.
