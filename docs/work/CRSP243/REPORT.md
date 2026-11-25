# CRSP243 report: dependency install study
Status: spec written, pushed. PR: see below.

## Answer
Docker uninstalled: crsp detects and reports it (agent `detectDocker`, `DetectEngine`, `detectWSL2`; doctor `machine.engine` text fix; Add runners greys Persistent). It installs nothing but crsp itself. Once an engine answers, crsp makes `crsp-pool:<key>` (ensure-image: local, registry by digest, local build) and `crsp-persistent-pool` + volumes (StartPersistent) on its own. `pool-refresh` is a dev-standards script, not crsp (OPS95 open).

## Delivered
- docs/features/dependency-setup/spec.md (flow, states, DEP1-DEP16, per OS and per runner type tables, 11 open decisions with defaults, tests, sources)
- docs/features/dependency-setup/changes/CRSP243.md

## Gaps
- `docs_check.py` / `docs_fold.py` / luismc-docs plugin not installed in this cloud container (plugins list empty). docs_check NOT run. Front matter copied from doctor spec; status `planned` is a guess. Run docs_check before merge. Generated files (features/index.md etc) not regenerated.
- Not verified by search: Portainer wizard, winget/Homebrew Docker behaviour, current Docker Desktop silent flag names. Marked in spec.
- No subagents used. No code changed.
