# Autostart entries

Reference copies of the per-user entries that `crsp tray` writes (tray R5.21).
The code in `apps/controller/internal/tray/autostart*.go` is the source; these files show the result.

- Linux desktop: `crsp-tray.desktop` goes in `$XDG_CONFIG_HOME/autostart` (default `~/.config/autostart`).
- macOS: `dev.luismc.crsp.tray.plist` goes in `~/Library/LaunchAgents`.
- Windows: value `crsp-tray` = `"<path>\crsp.exe" tray` under `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.
- Headless Linux: no entry (R5.22). Use `crsp open`.
