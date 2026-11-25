# TRAY7 report

Task: macOS and Linux update window and first-start window in the crsp look (TRAY5 D12, D19, D20).

## Status
- Step 1 (code): update window as a web page, first-start window, tray wiring. Built; unit tests green on Linux.
- Step 2: real-browser check on Linux, spec, content words, change note, docs_check, CI on all three OS. In progress.

## Decisions I took (nobody was there to ask; the owner can overrule)
- D19 (own browser profile) was left to "decide when built": yes. Profile folder `<user cache>/crsp/crsp-window-profile`, flags `--user-data-dir --window-size --no-first-run --no-default-browser-check --disable-extensions`. No Chromium-family browser: the update falls back to the console page (R5.43) and the first-start window is skipped.
- D20 left the choices open. I chose two: `Start at login` (on) and `Open the console` (on). Steps: Welcome, Your choices, All set. Shown once per user (marker file in the config folder), on macOS and Linux only; on Windows the installer is the first-start dialog.
- One engine for both windows: this process serves a page on 127.0.0.1 (secret path, Host check, header on press), opens it with the browser, and ends when a button closes it or the page goes quiet for 12 s (closed window = Hide).
- Windows keeps the native window; `crsp update-window --web` opens the web page there too (used to test).
- Second start: the first window is found through an address file and its address is opened again; whether the browser raises the old window is up to the browser (best effort).

## Not verified
- macOS: built and unit-tested in CI (macos-latest, cgo); never run on a real Mac.
- Windows: web page tested only through Go unit tests; the native window is unchanged.
