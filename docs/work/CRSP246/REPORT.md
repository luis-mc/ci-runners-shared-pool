# CRSP246 report (agent branch only, removed before ready PR)

Status: in progress, 2026-11-25.

## Blockers found
- `runs-on.com` and `betterstack.com` do not resolve in this session (network allow-list). Docs, pricing, changelog pages could NOT be read.
- Used instead: runs-on GitHub README (fetched, verified from README), WebSearch snippets (secondary, marked inferred).
- `docs_check.py` (luismc-docs plugin) is not installed on this machine. Searched filesystem: not found. Check not run.
- Owner action: allow `runs-on.com` in the environment network settings, then re-run to upgrade "inferred" rows to "verified from docs".

## Plan
1. docs/studies/README.md, 2. study.md, 3. index.html, 4. docs/README.md map, 5. remove report, open PR.

PR: (pending)
index.html: docs/studies/runs-on-vs-crsp/index.html
