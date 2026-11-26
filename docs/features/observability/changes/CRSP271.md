---
change: minor
task: CRSP271
---
Timing rules (R5.8, R5.9, R5.10): `internal/history/timing.go` splits GitHub wait and slot wait, counts the slot gate once, never gives a negative or zero-time duration, counts a re-run from its attempt start, and maps results with `startup_failure` and `timed_out` counted as failure. Wait reasons (R5.5): the 2-minute no-runner-with-labels rule and a test of every reason. Web console: one time format (R6.12), one shared clock that pauses while hidden and the status word, glyph and colour set (R5.6), and the test that the Overview and strip read one live store (R5.4).
