# TRAY7 report

Task: macOS and Linux update window and first-start window in the crsp look (TRAY5 D12, D19, D20).

## Status
- Step 1 (code): update window as a web page, first-start window, tray wiring. Built; unit tests green on Linux.
- Step 2: real-browser check done on Linux (screenshot ok, Update now fails cleanly without a service). Spec, content words, change note written. docs_check.py and docs_fold.py: the luismc-docs plugin is not in this sandbox, so the change note was written by hand in the same format and docs_check was NOT run. CI next.

## Decisions I took (nobody was there to ask; the owner can overrule)
- D19 (own browser profile) was left to "decide when built": yes. Profile folder `<user cache>/crsp/crsp-window-profile`, flags `--user-data-dir --window-size --no-first-run --no-default-browser-check --disable-extensions`. No Chromium-family browser: the update falls back to the console page (R5.43) and the first-start window is skipped.
- D20 left the choices open. I chose two: `Start at login` (on) and `Open the console` (on). Steps: Welcome, Your choices, All set. Shown once per user (marker file in the config folder), on macOS and Linux only; on Windows the installer is the first-start dialog.
- One engine for both windows: this process serves a page on 127.0.0.1 (secret path, Host check, header on press), opens it with the browser, and ends when a button closes it or the page goes quiet for 12 s (closed window = Hide).
- Windows keeps the native window; `crsp update-window --web` opens the web page there too (used to test).
- Second start: the first window is found through an address file and its address is opened again; whether the browser raises the old window is up to the browser (best effort).

## Not verified
- macOS: built and unit-tested in CI (macos-latest, cgo); never run on a real Mac.
- Windows: web page tested only through Go unit tests; the native window is unchanged.

## CI
tray-smoke on branch head f4e77dc: green on ubuntu, windows and macos (build + `go test ./internal/updatewindow ./internal/tray` on all three). Full suite: the ready PR's CI.
