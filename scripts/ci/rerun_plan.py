#!/usr/bin/env python3
"""CRSP318: narrow the Go tests of a fix push on a dispatch or agent run.

    rerun_plan.py --repo O/R --branch BR --run-id ID --shard K/N --job-name NAME --artifact NAME --out FILE

A workflow_dispatch run on a branch whose previous dispatch run was red re-runs only the Go tests that
failed there (this shard's) plus the tests of the packages whose Go files changed since that run's head.
Shard 1 carries the changed packages, so N shards do not run them N times. The decision is
scripts/delivery/rerun_tests.py (valves, unreadable reports and unmapped changes run the whole job).
Writes FILE with {"mode", "reason", "invocations"}; mode "narrow" means go-test.sh runs only those
invocations (GO_TEST_RERUN=FILE). Any error writes mode "whole": today's behaviour. Always exits 0.
A narrowed green run is not the merge gate: the ready PR's CI is. Python 3.8+, standard library only.
"""
from __future__ import annotations

import argparse
import json
import subprocess
import sys
import tempfile
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "delivery"))
import rerun_tests  # noqa: E402

DOCS = ["docs/**", "**/*.md"]


def go_areas(changed):
    """One area per changed Go package directory: its Go files map to its own tests. Other files have
    no area, which runs the whole job."""
    areas = {}
    for f in changed:
        if f.endswith(".go"):
            d = f.rsplit("/", 1)[0] if "/" in f else ""
            pre = d + "/" if d else ""
            areas[d or "root"] = {"paths": [pre + "*.go"], "jobs": [], "tests": [pre + "*_test.go"]}
    return {"docs": DOCS, "areas": areas}


def plan(shard_k, prev, changed, test_files):
    """rerun_tests.decide for Go, with the changed-package tests kept on shard 1 only."""
    res = rerun_tests.decide("go", "go-race", prev, changed, None, go_areas(changed), test_files, None)
    if res["mode"] == "narrow" and shard_k != 1:
        extra = set(res["changed_tests"])
        res["invocations"] = [i for i in res["invocations"] if not (len(i) == 1 and i[0] in extra)]
        if not res["invocations"]:
            res["mode"], res["reason"] = "narrow", "no failed test in this shard"
    return res


def _gh(args):
    r = subprocess.run(["gh", *args], capture_output=True, text=True)
    if r.returncode != 0:
        raise RuntimeError(r.stderr.strip() or "gh failed")
    return r.stdout


def previous(repo, branch, run_id, job_name, artifact):
    """(prev dict for rerun_tests, previous head sha) from the newest finished dispatch run on the branch."""
    q = f"repos/{repo}/actions/workflows/ci.yml/runs?branch={branch}&event=workflow_dispatch&per_page=10"
    runs = json.loads(_gh(["api", q]))["workflow_runs"]
    old = next((r for r in runs if str(r["id"]) != str(run_id) and r["status"] == "completed"), None)
    if not old:
        return None, None
    jobs = json.loads(_gh(["api", f"repos/{repo}/actions/runs/{old['id']}/jobs?per_page=100"]))["jobs"]
    job = next((j for j in jobs if j["name"] == job_name), None)
    concl = job["conclusion"] if job else "missing"
    report = None
    if concl == "failure":
        with tempfile.TemporaryDirectory() as d:
            try:
                _gh(["run", "download", str(old["id"]), "-R", repo, "-n", artifact, "-D", d])
                report = (Path(d) / "go-test.json").read_text(encoding="utf-8", errors="replace")
            except (RuntimeError, OSError):
                report = None
    return {"conclusion": concl, "report": report, "log": None}, old["head_sha"]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    for a in ("repo", "branch", "run-id", "shard", "job-name", "artifact", "out"):
        ap.add_argument("--" + a, required=True)
    a = ap.parse_args(argv)
    try:
        prev, sha = previous(a.repo, a.branch, a.run_id, a.job_name, a.artifact)
        if not prev:
            res = {"mode": "current", "reason": "no earlier dispatch run on this branch", "invocations": []}
        else:
            subprocess.run(["git", "fetch", "--quiet", "origin", sha], capture_output=True)
            diff = subprocess.run(["git", "diff", "--name-only", "--no-renames", sha, "HEAD"],
                                  capture_output=True, text=True, check=True).stdout.split()
            files = subprocess.run(["git", "ls-files"], capture_output=True, text=True, check=True).stdout.split()
            res = plan(int(a.shard.split("/")[0]), prev, diff, files)
    except Exception as exc:  # any doubt: the whole job
        res = {"mode": "whole", "reason": f"plan failed: {exc}", "invocations": []}
    Path(a.out).write_text(json.dumps(res), encoding="utf-8")
    print(f"rerun mode={res['mode']}: {res['reason']}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
