"""CRSP349: carry_adjust.py keeps the build job whenever the image or the e2e shards need its binary."""
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "delivery"))
import carry_adjust as c  # noqa: E402

PROOF = {"sha": "a" * 40, "run_url": "https://example.test/run/1"}


def adjust(plan, carried, changed=False):
    return c.adjust(plan, carried, "r", lambda sha: changed)


class CarryAdjust(unittest.TestCase):
    def test_empty_plan_changes_nothing(self):
        plan, carried, _ = adjust({}, {})
        self.assertEqual((plan, carried), ({}, {}))

    def test_build_stays_carried_when_nothing_needs_it(self):
        plan, carried, _ = adjust({"run_build": "false", "run_image": "false", "run_e2e-shards": "false"}, {"build": PROOF})
        self.assertEqual(plan["run_build"], "false")
        self.assertIn("build", carried)

    def test_e2e_shards_pull_the_build_in(self):
        plan, carried, reason = adjust({"run_build": "false", "run_e2e-shards": "true"}, {"build": PROOF})
        self.assertEqual(plan["run_build"], "true")
        self.assertNotIn("build", carried)
        self.assertIn("build runs", reason)

    def test_image_pulls_the_build_in(self):
        plan, carried, _ = adjust({"run_build": "false", "run_image": "true", "run_e2e-shards": "false"}, {"build": PROOF})
        self.assertEqual(plan["run_build"], "true")
        self.assertNotIn("build", carried)

    def test_changed_build_workflow_runs_the_build(self):
        plan, carried, reason = adjust({"run_build": "false", "run_image": "false", "run_e2e-shards": "false"}, {"build": PROOF}, changed=True)
        self.assertEqual(plan["run_build"], "true")
        self.assertNotIn("build", carried)
        self.assertIn("build.yml", reason)

    def test_other_carried_jobs_are_kept(self):
        _, carried, _ = adjust({"run_build": "true"}, {"lint": PROOF, "types": PROOF})
        self.assertEqual(sorted(carried), ["lint", "types"])


if __name__ == "__main__":
    unittest.main()
