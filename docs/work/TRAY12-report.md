# TRAY12: native window for lifecycle progress and questions, tray only notifies

Branch TRAY12-native-window-lifecycle. Areas: internal/updatewindow, internal/tray, internal/trayserver (unchanged), one line in cmd/crsp/main.go.

## Done
- Words table: internal/tray/words.go (dev-standards words.json at 89711a43). Words the table lacks (waiting, rolling back, skipped, rolled back without a retry time, Hide/Close/Cancel update) are kept apart in the same file, for the owner to move into the shared table.
- Update window: new states verifying, testing, rolling_back, rolled_back, refused, skipped, plus waiting, downloading with progress, installing, updated (What's new, Close). Stage fields `previous` and `retry_at` added to tray.StageInfo (OPS68 writes them).
- Remove question: `crsp lifecycle-window remove` (internal/updatewindow/lifecycle.go, remove.go). Keep my data / Delete my data too, none chosen, Enter presses nothing, Remove disabled until a choice, Cancel. Exit 10 keep, 11 delete, 1 cancel. Registration: one `if` in cmd/crsp/main.go (not in internal/cli, so OPS69 does not conflict). Flags: --product, --size (GB), --path; size measured from --path when absent.
- Tray: dialog_*.go, AskUpdate, Asker, Installer removed; the Windows notice is a toast, not a MessageBox. Offer opens the console or the native window, else one notice. One notice per event per 24 h (tray.Notices).
- Docs: tray spec R5.31 to R5.33, change notes for tray and cli.

## Checks
- go test -race ./internal/tray ./internal/updatewindow ./internal/trayserver ./cmd/crsp: pass.
- GOOS=windows go vet (internal/..., cmd/crsp) and GOOS=windows GOARCH=arm64 go vet: pass.
- golangci-lint (linux and windows) on the touched packages: 0 issues.
- docs_check --strict: 0 errors, 0 warnings.
- tray-modal.mjs: run against a Go-adapted copy (CONFIG: .go files, NoticeWindow = 24 * time.Hour): ok. Go equivalent kept in internal/tray/modal_guard_test.go.

## Not done / cannot be done here
- states-render.mjs loads HTML pages in Chromium; a native Win32 window has no page. Equivalent: Clipped() at 100, 125, 150, 200% with a fake font (unit tests, all views including remove) and `crsp update-window --layout-check` with the real Consolas font. The real-font measure runs on Windows only; no Windows runner here. Needs the Windows CI lane (OPS69) to run it and keep the screenshots.
- The drawn window (radio, disabled button, arrow keys) is compiled and vetted, not run.
- `confirm_disabled_until` ("keep or purge chosen") is not shown: as a sentence it does not read well. Owner to pick wording.
- Tab does not visit the radio choices; Up/Down/Left/Right choose, Tab goes over the buttons.

CODE DONE (sha in the final commit)

| ID | status | evidence |
|---|---|---|
| LC-22 (tray part) | pass | modal_guard_test.go, tray-modal.mjs adapted run |
| LC-24 (window part) | pass, real-font run pending | lifecycle_test.go, layout_test.go |
| LC-30 (window part) | pass, real-font run pending | TestNothingIsClippedAtAnyDPI, TestWindowIsSizedFromItsContent |
| tray-modal guard | pass | adapted run and Go test |
| states-render guard | not applicable to a native window | see above |
