# CRSP266 report (spec debug-access, second pass)
- Brought spec from origin/CRSP266-debug-access, fixed cross-refs against main: runners R5.4/R5.26/R5.2 -> R5.21/R5.7/R5.8, caches R5.46 dropped, public guard -> github-integration R5.41/R5.47, section 5.13 -> R5.82, cli link added, notifications R5.1 pointer, last_verified 2026-11-26, artifact link blanked.
- docs_check.py / docs_index.py NOT run: luismc-docs plugin not installed in this cloud session. Generated files (features/index.md, docs-map.json, llms.txt, index.html) NOT rebuilt. Owner/CI must run `docs_index.py build` and `docs_check.py`.
- Change note written by hand (docs_fold.py missing).
- Not verified: test paths do not exist (all planned); runners R5.28 Windows rows, R5.29 macOS cited as before.
