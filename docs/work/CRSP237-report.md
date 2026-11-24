# CRSP237 report: flaky TestFontsMeasureAfterTheScreenDCIsReleased

Root cause: `fonts.metrics().Measure` called `GetTextExtentPoint32W` and ignored its return value. The call returns 0 (and leaves the size at 0) when GDI refuses it, which happens under GDI/desktop-heap pressure on a busy or headless runner session. The CI failure (run 45079111227, windows-latest) was a single line of the `refused` view measuring 0 px at 144 dpi while every other call answered. The DC is owned by the fonts for their whole life (not released), so this is not a released-DC bug; the failing call is transient. The `avgW` fallback was computed in `newFonts` but never used. Fix: `Measure` checks the return and falls back to `len(runes) * avgW`, so a refused call gives a plausible width instead of 0 and the layout is deterministic. No sleeps, retries or skips. The test is unchanged and still proves Measure answers after `newFonts` released its screen DC. Cannot run Windows here: cross-built with `GOOS=windows go vet` and `go test -c`; CI is the proof.

CI run 45286549322 on cbfe6978e4ae333a5838d48db0d2b56c07e05cc7: success, Go tests (windows-latest) passed. One green run cannot prove a flake gone; the unchecked return is closed.

CODE DONE cbfe6978e4ae333a5838d48db0d2b56c07e05cc7
