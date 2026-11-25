# CRSP286 report (perf: console run and job lists, audit P-1 to P-4)

Status: in progress. Branch CRSP286-perf-lists.

Plan
- P-1 ListRuns: filter + page first (expression index on the sort key, LIMIT), then one grouped count query for the page rows only. Total = cheap count(*) on the filter. State filter uses EXISTS on jobs instead of 4 counts per run.
- P-2 ListJobs: explicit list columns, no SELECT *.
- P-3 migration 00046: jobs(repository_id, run_id, run_attempt, state), runs conclusion / repository_id / head_branch, runs sort key expression index. Checked with EXPLAIN QUERY PLAN.
- P-4 separate read pool (query_only, more conns) and a single write conn; WAL stays.
- Benchmark/test with 10k runs, 100k jobs; p95 under 200 ms.
