---
change: minor
task: CRSP262
---
Adds metrics export (gap G2 of the runs-on study): a Prometheus /metrics page and an OpenTelemetry (OTLP over HTTP) push of one metric set (runners by state, queue wait, job duration, fallback count, cache hit rate, machine uptime), off by default, on 127.0.0.1 unless the owner exposes it, with an optional key kept in the secret store. A Settings, Console, Metrics screen gives the switches and a copy-paste scrape config. Requirements R5.70 to R5.77. Tested on Windows and Linux; macOS is compiled and unit-tested in CI, not run on a real Mac.
