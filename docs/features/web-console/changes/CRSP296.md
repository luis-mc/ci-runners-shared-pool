---
change: minor
task: CRSP296
---
The console loads as one script and one stylesheet, each with a brotli and a gzip copy made at build time; the controller sends the copy the browser accepts, keeps named files for good (immutable) and revalidates the page shell with an ETag (R6.14, UX-87). The first-JavaScript check now builds the production bundle.
