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


LIFECYCLE = (ROOT / ".github" / "workflows" / "lifecycle.yml").read_text(encoding="utf-8")
TABLE = json.loads((ROOT / "scripts" / "delivery" / "ci-shards.json").read_text(encoding="utf-8"))


def job(text, name):
    """The text of one top-level job of a workflow."""
    m = re.search(r"^  %s:\n(.*?)(?=^  [a-z0-9-]+:\n|\Z)" % re.escape(name), text, re.S | re.M)
    assert m, name
    return m.group(1)


class PassRecords(unittest.TestCase):
    def test_every_costly_lane_lists_inputs(self):
        shards = {s["id"]: s for s in TABLE["shards"]}
        self.assertEqual(set(shards), {"image", "test-os", "msi"})
        for sid, s in shards.items():
            self.assertTrue(s.get("inputs"), sid)
            self.assertTrue(s.get("commands"), sid)

    def test_image_and_os_lanes_wait_for_the_record_check(self):
        for name in ("image", "test-os"):
            body = job(CI, name)
            self.assertIn("lane-records", body, name)
            self.assertRegex(body, r"needs\.lane-records\.outputs\.\w+ != 'true'", name)

    def test_msi_lanes_skip_on_a_record_and_the_release_check_still_runs(self):
        self.assertIn("use_records: true", job(CI, "lifecycle"))
        self.assertIn("needs.records.outputs.hit != 'true'", job(LIFECYCLE, "windows"))
        self.assertIn("needs: [records, windows]", job(LIFECYCLE, "release-check"))
        self.assertIn("if: ${{ always() }}", job(LIFECYCLE, "release-check"))

    def test_a_manual_run_never_reads_records(self):
        self.assertIn('"$EVENT" != "workflow_dispatch"', job(CI, "lane-records"))
        self.assertIn('[ "$USE_RECORDS" = "true" ]', job(LIFECYCLE, "records"))

    def test_only_the_records_job_may_write_contents(self):
        self.assertEqual(len(re.findall(r"^\s+contents: write$", CI, re.M)), 1)
        self.assertIn("contents: write", job(CI, "pass-records"))
        self.assertNotIn("contents: write", LIFECYCLE)

    def test_records_are_read_from_main_only(self):
        # to-result reads only the default branch (DL-118); nothing here passes --read-from
        self.assertNotIn("--read-from", CI + LIFECYCLE)


if __name__ == "__main__":
    unittest.main()
