# CRSP253 report

Branch CRSP253-doctor-pool. Started 2026-11-25.

## Scope mismatch (owner please read)
The task text says "pool containers: image, layers, warm pool, tool cache. Closes R5.37, R5.38". In the doctor spec:
- R5.31 (container checks: image.current, layer.tools, toolcache) is already Built (CRSP53). Only a warm-pool check is missing.
- R5.37 is `machine.disk` warn before the floor; R5.38 is `security.reach`. Neither is about containers.
Decision (no one to ask on a scheduled run): build all of it, all behind interfaces with fakes:
1. `container.warm` (new, R5.31 extension).
2. R5.37: warn at floor + 20%, unknown disk is skip, names largest crsp folders.
3. R5.38: `security.reach` info per repo, warn on loopback host listener on a machine running container jobs.
SECURITY.md section (runners R5.64) is not in this task; the link target is named in the finding only.

## Platforms
Windows and Linux paths verified in CI. macOS code is compiled and unit-tested in CI with fakes; NOT run on a real Mac.

## Steps
- [ ] container.warm
- [ ] R5.37
- [ ] R5.38
- [ ] spec + change note + docs_check
- [ ] CI on branch, PR
