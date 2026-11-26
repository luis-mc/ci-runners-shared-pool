# CRSP311 report

Solution S8: `.githooks/pre-push` project gates now run `prettier --check` on the files the branch changed (diff against upstream or origin/main, deleted files skipped, `--ignore-unknown`, so .prettierignore still applies). It sits inside the existing node_modules check, so the missing-node_modules message and the DL-88 e2e gate-result check are untouched. Nothing runs when no prettier-type file changed.

gofmt (whole repo, fast) and golangci-lint (only if installed, else a skip message) were already in the hook; left as is.

Tested by hand: a mis-formatted .ts file in the diff makes the same command return 123. The repo has no hook unit tests. The stack-profile deviation line that lists the hook gates now names the prettier check.
