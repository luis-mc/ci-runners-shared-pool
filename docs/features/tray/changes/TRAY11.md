---
change: minor
task: TRAY11
---
An update on Windows says when another installer holds Windows Installer (stage waiting_installer, window 'Waiting for another installer'), retries every 15 s until it can install, shows 'Restarting the service' only once msiexec has finished, and fails or retries an attempt whose msiexec ended without replacing the service (R5.53, R5.54).
