---
change: patch
task: OPS92
---
A Windows runner add ran no PowerShell step: the script sent to powershell -Command - on stdin had no closing blank line, so PowerShell exited 0 and did nothing. Every such script now ends with a blank line and holds none inside; the leftovers scripts end the same way.
