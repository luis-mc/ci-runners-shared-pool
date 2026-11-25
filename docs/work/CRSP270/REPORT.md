# CRSP270 report: dependency install step two (plan row O)

Branch `CRSP270-dep-install-2`, from origin/main at 149739b (row N CRSP265 merged).
Started 2026-11-25. Systems: Windows, Linux, macOS (macOS built and CI-tested, not run on a real Mac).

## Scope read from the spec (docs/features/dependency-setup/spec.md, section 11)
Already built by CRSP248/CRSP265: DEP4, DEP5, DEP6, DEP7, DEP8, DEP10, DEP11 (consent, pinned installers, events, reboot, verify, undo, idempotent).
Open after step one, in this task:
- DEP13 full door behaviour: Setup shows the note on the controller's own machine only.
- DEP12 per target machine beyond the agent link (join over SSH: offer after join; offline: no offer).
- Time budget: a run past 3 times its typical time says so and offers Stop (spec 6).
- Resume after a restart only when "Restart now" was used (known gap).
- DEP15 offline machines (controller fetches and checks, agent receives): needs a new protocol message and file serving.
- Conflicting unofficial Docker packages removed only with a yes: the protocol cannot carry the yes yet.

## Plan
1. Brief one agent (all items share depsetup, agent link and the wizard files). Order: time budget, DEP13 doors, DEP12 SSH-joined, resume gap.
2. DEP15 and the conflict yes are done only if the protocol change is small; else written as not built.
3. Spec section 11 and change note; docs_check.
4. Local smoke of touched suites; CI on the branch via ci_watch.py.

## Environment notes
- `luismc-docs` plugin scripts (`docs_fold.py`, `docs_check.py`) are not installed in this cloud session: the change note is written by hand in the same file format; docs_check could not run.

## Log
- 2026-11-25: branch created and pushed.
