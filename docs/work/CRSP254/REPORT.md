# CRSP254 report (branch only)
Rescue of old CRSP254-runner-gaps as CRSP254-runner-gaps-2; cherry-pick applied clean on main 351a6b56. offline.go untouched (CRSP326 safe).
Built: R5.34 mirror fallback, R5.63 limits/network/check, R5.64 SECURITY.md section, R6.5 budget check; R5.92/R5.93 tests named.
Local: go build ok, go vet ok, go test ./internal/runners ok. No web change.
Not verified: docs_check.py (not in sandbox, CI runs it), R5.63 wiring into internal/backend/container, real engine/Mac start times.
