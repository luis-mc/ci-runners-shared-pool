---
change: patch
task: OPS82
---
Test guardrails G1: Python CI tests run under pytest with a 10 s per-test timeout (pytest.ini, pinned pytest-timeout); packages over 60 s run in chunks with the 60 s limit instead of one 150 s run; the Windows/macOS job is named "Go tests".
