# CRSP255 report

Task: shared per-machine tool cache (plan row C). Branch CRSP255-tool-cache.

## Done
- `internal/caches/toolcache`: Store (Install by stage + one rename, Lookup touch, Evict LRU with MinAge guard and trash rename, Wipe), Platform (Unix = Linux and macOS mode 1777, Windows = icacls plan), FS interface, `Checker` interface for the doctor (row A wires it; no doctor code touched).
- `internal/backend/container/toolcache.go`: read-write bind mount at /crsp-tool-cache, `CRSP_TOOL_CACHE`, `CI_TOOLS_DIR` when no lane gives it. `PoolSpec.ToolCacheHost`, `Config.ToolCacheRoot` (pool start copies it). Runners package not touched.
- Spec: caches R5.61 to R5.64, test table, code map, change note `docs/features/caches/changes/CRSP255.md` (written by hand in the format of the other notes: `docs_fold.py` is not in this sandbox).

## Verified
- Linux: `go test ./internal/caches/... ./internal/backend/container/`, `go test -race -count=3` of toolcache: green.
- Windows and macOS: `GOOS=windows go vet`, `GOOS=darwin go vet`: clean. Not run on Windows or a Mac. Windows ACL is a plan checked through a fake runner.
- docs_check.py: NOT RUN (luismc-docs plugin not in sandbox).

## Not done / open
- Agent start that sets `Config.ToolCacheRoot`, calls Prepare, runs Evict on a timer; setting `tool_cache_cap_gib`.
- Doctor check using `Checker` (row A).
- Machines R5.60 (free-disk floor: default 20 GiB, percentage form, hold counting against the gate wait) is NOT part of this change: it is the disk floor, not the tool cache, and lives in agent/runners code I was told not to touch. Machines spec not edited.
