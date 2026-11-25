# CRSP254 report
Done: R5.34 mirror fallback (mirror.go), R5.63 limits/network/check (joblimits.go), R5.64 SECURITY.md section, R6.5 budget check (starttime.go), R5.92/R5.93 tests named in spec table. Spec + change note written.
Local: go test ./internal/runners ok; go vet for windows/darwin ok.
Not done: docs_fold.py / docs_check.py not present in this sandbox (note written by hand in the existing format; run docs_check in CI/owner). Wiring R5.63 into internal/backend/container (outside owned scope). Real Mac and real-engine start-time not run.
Scope note: SECURITY.md edited (R5.64 needs it).
