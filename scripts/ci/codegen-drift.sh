#!/usr/bin/env bash
# Codegen drift gate (STACKSPEC 4.1 / 13.1 gate 2): regenerate every generated
# file and fail if any differs from what is on disk. It compares content
# hashes, so it works on a tree with uncommitted or untracked files.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

GENERATED_DIRS=(apps/controller/internal/api/gen apps/controller/internal/api/client apps/controller/internal/store/sqlcgen apps/controller/internal/gen packages/contract/ts/generated)

snapshot() {
  for d in "${GENERATED_DIRS[@]}"; do
    [ -d "$d" ] && find "$d" -type f
  done | LC_ALL=C sort | while IFS= read -r f; do
    printf '%s  %s\n' "$(tr -d '\r' < "$f" | sha256sum | cut -d' ' -f1)" "$f"
  done
}

before=$(snapshot)
go tool task codegen
after=$(snapshot)

if [ "$before" != "$after" ]; then
  echo "codegen drift: regenerated files differ from the tree." >&2
  diff <(echo "$before") <(echo "$after") >&2 || true
  echo "Run 'go tool task codegen' and commit the result." >&2
  exit 1
fi
if [ -z "$after" ]; then
  echo "codegen drift: no generated files found; an empty check would pass for the wrong reason." >&2
  exit 1
fi
echo "codegen drift: none"
