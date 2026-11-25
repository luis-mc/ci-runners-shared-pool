# CRSP264 report: free-form size labels (plan row U, gap G9)

Branch: CRSP264-size-labels. Status: in progress.

## Done
- Step 1: `internal/sizelabel` parser (cpu=N, ram=N or ramNgb), validation with clear errors, per-machine fit, placement (smallest machine that holds the job), container limit clamp. Narrow calls: `runners.ValidateLabels` accepts valid size labels; container `limitsFor` lowers caps by the label. Tests pass on Linux.

## Platforms
Windows and Linux: built, tested locally where possible (Linux here; Windows by CI only in this cloud session). macOS: built and CI-tested (macos-latest already in the `test-os` matrix), NOT run on a real Mac.
