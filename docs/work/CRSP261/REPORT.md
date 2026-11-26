# CRSP261 report (branch CRSP261-cloud-overflow-2)
Design: one setting `overflow_label`; order repo hosted_labels > overflow_label > GitHub default. Simpler than the old branch (no global cap, no new screen).
Built: settings, repos fallback + variable write, API, openapi, Go gen, config export, spec, change note.
Not verified: docs_check.py/docs_fold.py (luismc-docs plugin absent in sandbox; note written by hand); pnpm codegen (node version; TS schema edited by hand); console field not added; no CI run yet.
