"""CRSP209: the Go tier list and the e2e weights stay true to the tree."""
import json
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
        self.assertEqual({k for k in tiers if not k.startswith("_")}, {"integration", "slow", "chunks"})

    def test_every_slow_package_has_a_chunk_count(self):
        tiers = json.loads((ROOT / "scripts/ci/go-tiers.json").read_text())
        self.assertEqual(sorted(tiers["chunks"]), sorted(tiers["slow"]))
        for pkg, n in tiers["chunks"].items():
            self.assertGreaterEqual(n, 2, pkg)


class E2eWeights(unittest.TestCase):
    def test_weighted_files_exist(self):
        w = json.loads((ROOT / "scripts/ci/e2e-weights.json").read_text())["seconds"]
        for f in w:
            self.assertTrue((ROOT / f).is_file(), f"{f} has a weight but does not exist")

    def test_every_shard_gets_files(self):
        files = sorted(
            p.relative_to(ROOT).as_posix()
            for p in (ROOT / "e2e/tests").rglob("*.spec.ts")
            if p.name != "ux-routes.spec.ts"
        )
        got = []
        for k in range(1, 5):
            out = subprocess.run(
                [sys.executable, str(ROOT / "scripts/delivery/shard_balance.py"), "--shard", f"{k}/4",
                 "--timings", str(ROOT / "scripts/ci/e2e-weights.json"), "--files-from", "-"],
                input="\n".join(files), capture_output=True, text=True, check=True, cwd=ROOT,
            ).stdout.split()
            self.assertTrue(out, f"shard {k} has no files")
            got += out
        self.assertEqual(sorted(got), files)


if __name__ == "__main__":
    unittest.main()
