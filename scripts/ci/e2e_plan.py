#!/usr/bin/env python3
"""CRSP313: split the e2e suite over the shards by recorded time.

    e2e_plan.py --shard K/N --what files|slices [--files-from FILE|-] [--weights FILE] [--slices S]

A shard runs its spec files in one Playwright run (4 workers) and its slices of
ux-routes.spec.ts (`--shard=a/S`, one run per slice). The old split gave every shard a
quarter of the routes spec and balanced the files by their serial seconds, so the shard that got
the 223 s a11y file ran 13 min and another 6. Here the cost of a shard is what it really takes:

    files wall = max(longest file, sum of file seconds / workers)     (workers run files side by side)
    slices     = slices * (routes_wall_seconds / S + SLICE_OVERHEAD)

Files go longest first to the shard whose cost grows least (ties: lower shard), so a shard that
is held up by one long file takes the short ones for free. Then the S slices go one by one to the
cheapest shard. Every file and every slice 1..S is given to exactly one shard: nothing is
dropped. Deterministic: the same input always gives the same plan. A file with no recorded time
weighs the median of the recorded ones. Python 3.8+, standard library only.
"""
from __future__ import annotations

import argparse
import json
import statistics
import sys
from pathlib import Path

WORKERS = 4
SLICES = 16
SLICE_OVERHEAD = 6.0  # seconds: server start and Playwright start of one more run


def files_wall(seconds: list[float]) -> float:
    return max(max(seconds), sum(seconds) / WORKERS) if seconds else 0.0


def place_slices(base, slices, slice_cost):
    cost = list(base)
    mine = [[] for _ in base]
    for s in range(1, slices + 1):
        k = min(range(len(base)), key=lambda i: (cost[i], i))
        mine[k].append(s)
        cost[k] += slice_cost
    return mine, cost


def plan(files, timings, routes_wall, shards, slices):
    known = [timings[f] for f in files if f in timings]
    fill = statistics.median(known) if known else 1.0
    w = {f: timings.get(f, fill) for f in sorted(set(files))}
    slice_cost = routes_wall / slices + SLICE_OVERHEAD
    # Start: longest first onto the shard with the fewest file seconds.
    got = [[] for _ in range(shards)]
    tot = [0.0] * shards
    for f in sorted(w, key=lambda f: (-w[f], f)):
        k = min(range(shards), key=lambda i: (tot[i], i))
        got[k].append(f)
        tot[k] += w[f]

    def score(g):
        base = [files_wall([w[x] for x in grp]) for grp in g]
        _, cost = place_slices(base, slices, slice_cost)
        return (max(cost), sum(c * c for c in cost))

    # Then move or swap files while the slowest shard gets faster (deterministic order).
    best = score(got)
    improved = True
    while improved:
        improved = False
        for f in sorted(w):
            src = next(i for i in range(shards) if f in got[i])
            for dst in range(shards):
                if dst == src:
                    continue
                for g in [x for x in got[dst]] + [None]:
                    trial = [list(x) for x in got]
                    trial[src].remove(f)
                    trial[dst].append(f)
                    if g is not None:
                        trial[dst].remove(g)
                        trial[src].append(g)
                    sc = score(trial)
                    if sc < best:
                        best, got, improved = sc, trial, True
                        break
                if improved:
                    break
            if improved:
                break
    base = [files_wall([w[x] for x in grp]) for grp in got]
    mine, cost = place_slices(base, slices, slice_cost)
    return [sorted(g) for g in got], mine, cost


def main(argv=None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--shard", required=True, help="K/N, K from 1")
    ap.add_argument("--what", choices=["files", "slices", "cost"], required=True)
    ap.add_argument("--files-from", default="-")
    ap.add_argument("--weights", default="scripts/ci/e2e-weights.json")
    ap.add_argument("--slices", type=int, default=SLICES)
    a = ap.parse_args(argv)
    k, n = (int(x) for x in a.shard.split("/"))
    if not 1 <= k <= n:
        print(f"bad shard {a.shard}", file=sys.stderr)
        return 2
    data = json.loads(Path(a.weights).read_text(encoding="utf-8"))
    text = sys.stdin.read() if a.files_from == "-" else Path(a.files_from).read_text(encoding="utf-8")
    files = [x.strip().replace("\\", "/") for x in text.splitlines() if x.strip()]
    got, mine, cost = plan(files, data["seconds"], float(data["routes_wall_seconds"]), n, a.slices)
    if a.what == "files":
        print("\n".join(got[k - 1]))
    elif a.what == "slices":
        print("\n".join(str(s) for s in mine[k - 1]))
    else:
        print(round(cost[k - 1]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
