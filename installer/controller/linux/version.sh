#!/usr/bin/env bash
# Maps the release version to a package version (operations R5.7).
#
#   version.sh <release-version> <run-number> deb|rpm|pkg
#
#   1.2.3, v1.2.3     -> 1.2.3
#   1.2.3-rc1         -> 1.2.3~rc1 (deb, rpm: sorts below 1.2.3); 1.2.3 (pkg)
#   edge-<sha> (or anything else) -> <next patch after the latest stable tag>~edge.<run>
#                                     (pkg: <next patch>.<run>)
#
# The latest stable tag is read from the checkout (fetch the tags). No stable tag
# yet: the base is 0.1.0. A later edge build sorts above an earlier one, and the
# stable release of that base sorts above every edge build of it.
set -euo pipefail
v="${1:?release version}"
run="${2:-0}"
kind="${3:-deb}"
v="${v#v}"
if [[ "$v" =~ ^([0-9]+\.[0-9]+\.[0-9]+)(-([0-9A-Za-z.-]+))?$ ]]; then
  core="${BASH_REMATCH[1]}"
  pre="${BASH_REMATCH[3]:-}"
  case "$kind" in
    pkg) echo "$core" ;;
    *) if [ -n "$pre" ]; then echo "${core}~${pre}"; else echo "$core"; fi ;;
  esac
  exit 0
fi
base="0.1.0"
last="$(git tag --list 'v*' --sort=-v:refname 2>/dev/null | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -n1 || true)"
if [[ "$last" =~ ^v([0-9]+)\.([0-9]+)\.([0-9]+)$ ]]; then
  base="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}.$((BASH_REMATCH[3] + 1))"
fi
case "$kind" in
  pkg) echo "${base}.${run}" ;;
  *) echo "${base}~edge.${run}" ;;
esac
