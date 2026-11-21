# OPS74 report: runner classes, admission, reservation, memory cap (back end only)

Branch `OPS74-classes-admission`. Steps (pushed after each):

1. Class table, presets, hard rules, hints, pool fit, lowering confirm: `internal/runners/sizeclass_table.go`. DONE
2. Memory cap behind one interface with fakes: `internal/memcap` (cgroup, job object, watcher). DONE
3. Admission, reservation, backfill: `internal/scheduler`. IN PROGRESS
4. Store, migration 00037, API, openapi, codegen. TODO
5. Spec R5.89 and up, change note. TODO
