#!/usr/bin/env python3
"""CRSP349: fit the carry-forward plan (carry_plan.py, DS287) to the jobs of ci.yml that share a build.

    PLAN='{"run_build":"false",...}' CARRIED='{"build":{"sha":...}}' REASON=... GITHUB_OUTPUT=file carry_adjust.py

Two rules, both only ever add work, never remove it:
  1. The base image and the e2e shards download the crsp binary that THIS run built, so the `build` job runs
     whenever either of them runs.
  2. carry_plan reads a change to another workflow file as no change for ci.yml. build.yml is called by ci.yml,
     so a change to it since the build's proof head runs `build` (and, by rule 1, what needs it).
A git error counts as a change. Always exits 0; with an empty plan nothing is carried and every job runs.
Writes `plan`, `carried` and `reason` to $GITHUB_OUTPUT. Standard library only.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys

CALLED_WORKFLOW = ".github/workflows/build.yml"
NEEDS_BUILD = ("e2e-shards", "image")


def adjust(plan: dict, carried: dict, reason: str, changed) -> tuple[dict, dict, str]:
    """Pure rule set. `changed(sha)` says whether build.yml differs between the proof head `sha` and HEAD."""
    plan, carried = dict(plan), dict(carried)

    def run_job(job: str) -> None:
        plan["run_" + job] = "true"
        carried.pop(job, None)

    build = carried.get("build")
    if build and changed(build.get("sha") or "HEAD"):
        run_job("build")
        reason += "; %s changed since the proof head: build runs" % CALLED_WORKFLOW
    if "build" in carried and any(plan.get("run_" + j) != "false" for j in NEEDS_BUILD):
        run_job("build")
        reason += "; the image or the e2e shards run: build runs"
    return plan, carried, " ".join(reason.split())


def git_changed(sha: str) -> bool:
    try:
        return subprocess.run(["git", "diff", "--quiet", sha, "HEAD", "--", CALLED_WORKFLOW]).returncode != 0
    except OSError:
        return True


def _load(name: str) -> dict:
    try:
        data = json.loads(os.environ.get(name) or "{}")
    except ValueError:
        return {}
    return data if isinstance(data, dict) else {}


def main() -> int:
    plan, carried, reason = adjust(_load("PLAN"), _load("CARRIED"), os.environ.get("REASON", ""), git_changed)
    lines = ["plan=%s" % json.dumps(plan, separators=(",", ":")),
             "carried=%s" % json.dumps(carried, separators=(",", ":"), sort_keys=True),
             "reason=%s" % reason]
    target = os.environ.get("GITHUB_OUTPUT")
    if target:
        with open(target, "a", encoding="utf-8") as fh:
            fh.write("\n".join(lines) + "\n")
    print("carry_adjust: carried " + (", ".join(sorted(carried)) or "nothing") + " | " + reason)
    return 0


if __name__ == "__main__":
    sys.exit(main())
