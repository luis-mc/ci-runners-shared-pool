"""CRSP375/CRSP376: ci.yml uses the shipped skip check and the lane pass records; the watcher config is set."""
import json
import pathlib
import re
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
CI = (ROOT / ".github" / "workflows" / "ci.yml").read_text(encoding="utf-8")


class SkipCheck(unittest.TestCase):
    def test_push_uses_the_shipped_skip_check(self):
        self.assertIn("python3 scripts/delivery/ci_skip_check.py", CI)
        self.assertNotIn("scripts/ci/ci_skip_check.py", CI)
        self.assertFalse((ROOT / "scripts" / "ci" / "ci_skip_check.py").exists())

    def test_a_manual_run_never_skips(self):
        self.assertRegex(CI, r'\*\) echo "skip=false" >> "\$GITHUB_OUTPUT"')

    def test_watcher_tests_what_will_merge_and_retries_infrastructure_once(self):
        w = json.loads((ROOT / ".delivery" / "config.json").read_text(encoding="utf-8"))["watcher"]
        self.assertEqual(w["merge_result"], "update")
        self.assertIs(w["infra_retry"], True)


if __name__ == "__main__":
    unittest.main()
