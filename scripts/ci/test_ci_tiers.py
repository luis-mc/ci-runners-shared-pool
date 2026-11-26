"""CRSP209: the Go tier list and the e2e weights stay true to the tree."""
import json
import re
import subprocess
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


class GoTiers(unittest.TestCase):
    def test_listed_packages_exist_and_are_in_one_tier(self):
        tiers = json.loads((ROOT / "scripts/ci/go-tiers.json").read_text())
        seen = {}
        for tier in ("integration", "slow"):
            for pkg in tiers[tier]:
                self.assertNotIn(pkg, seen, f"{pkg} is in two tiers")
                seen[pkg] = tier
                d = ROOT / pkg
                self.assertTrue(d.is_dir(), f"{pkg} is not a directory")
                self.assertTrue(list(d.glob("*_test.go")), f"{pkg} has no Go test")

    def test_only_two_tiers_are_listed(self):
        tiers = json.loads((ROOT / "scripts/ci/go-tiers.json").read_text())
        self.assertEqual({k for k in tiers if not k.startswith("_")}, {"integration", "slow", "chunks", "seconds"})

    def test_every_slow_package_has_a_chunk_count(self):
        tiers = json.loads((ROOT / "scripts/ci/go-tiers.json").read_text())
        self.assertEqual(sorted(tiers["chunks"]), sorted(tiers["slow"]))
        for pkg, n in tiers["chunks"].items():
            self.assertGreaterEqual(n, 2, pkg)


class GoShards(unittest.TestCase):
    """CRSP313: the Go race tests run in N shards; together they run every package and chunk once."""

    N = 4

    _packages = None

    @classmethod
    def _all_packages(cls):
        # `go list ./...` is slow on a loaded runner; run it once, not once per shard and tier.
        if cls._packages is None:
            cls._packages = subprocess.run(["go", "list", "./..."], capture_output=True, text=True, check=True, cwd=ROOT).stdout
        return cls._packages

    def _tiers(self, tier, shard=None):
        args = ["node", str(ROOT / "scripts/ci/go-tiers.mjs"), tier] + ([shard] if shard else [])
        return subprocess.run(args, input=self._all_packages(), capture_output=True, text=True, check=True, cwd=ROOT).stdout.splitlines()

    def test_shards_cover_every_package_and_chunk_once(self):
        for tier in ("unit", "integration", "chunks"):
            whole = sorted(x for x in self._tiers(tier) if x)
            got = []
            for k in range(1, self.N + 1):
                got += [x for x in self._tiers(tier, f"{k}/{self.N}") if x]
            self.assertEqual(sorted(got), whole, tier)
            self.assertTrue(whole, tier)

    def test_every_shard_has_work(self):
        for k in range(1, self.N + 1):
            n = sum(len([x for x in self._tiers(t, f"{k}/{self.N}") if x]) for t in ("unit", "integration", "chunks"))
            self.assertGreater(n, 0, f"shard {k}")

    def test_workflow_runs_all_shards(self):
        wf = (ROOT / ".github/workflows/ci.yml").read_text()
        self.assertIn("GO_TEST_SHARD: ${{ matrix.shard }}/%d" % self.N, wf)
        self.assertIn("shard: [1, 2, 3, 4]", wf)


class E2ePlan(unittest.TestCase):
    """CRSP313: files and ux-routes slices go to the shards by recorded time; nothing is dropped."""

    def _run(self, k, what, files):
        return subprocess.run(
            [sys.executable, str(ROOT / "scripts/ci/e2e_plan.py"), "--shard", f"{k}/4", "--what", what, "--files-from", "-"],
            input=chr(10).join(files), capture_output=True, text=True, check=True, cwd=ROOT,
        ).stdout.split()

    def test_every_file_and_slice_runs_exactly_once(self):
        files = sorted(
            p.relative_to(ROOT).as_posix()
            for p in (ROOT / "e2e/tests").rglob("*.spec.ts")
            if p.name != "ux-routes.spec.ts"
        )
        got, slices = [], []
        for k in range(1, 5):
            got += self._run(k, "files", files)
            slices += self._run(k, "slices", files)
        self.assertEqual(sorted(got), files)
        wf = (ROOT / ".github/workflows/ci.yml").read_text()
        n = int(re.search(r'--shard="\$s/(\d+)"', wf).group(1))
        self.assertEqual(sorted(int(s) for s in slices), list(range(1, n + 1)))

    def test_slowest_shard_is_close_to_the_fastest(self):
        files = sorted(
            p.relative_to(ROOT).as_posix()
            for p in (ROOT / "e2e/tests").rglob("*.spec.ts")
            if p.name != "ux-routes.spec.ts"
        )
        costs = [int(self._run(k, "cost", files)[0]) for k in range(1, 5)]
        self.assertLess(max(costs), 1.25 * min(costs), costs)


class E2eWeights(unittest.TestCase):
    def test_weighted_files_exist(self):
        w = json.loads((ROOT / "scripts/ci/e2e-weights.json").read_text())["seconds"]
        for f in w:
            self.assertTrue((ROOT / f).is_file(), f"{f} has a weight but does not exist")


if __name__ == "__main__":
    unittest.main()
