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


GATE = json.loads((ROOT / "scripts" / "delivery" / "gate.json").read_text(encoding="utf-8"))
GO_TEST = (ROOT / "scripts" / "ci" / "go-test.sh").read_text(encoding="utf-8")


class Quarantine(unittest.TestCase):
    def test_the_blocking_run_skips_and_the_other_step_runs_only_the_listed_tests(self):
        self.assertIn('-skip "^($qnames)', GO_TEST)
        self.assertIn('-run "^($qnames)', GO_TEST)
        self.assertIn("GO_TEST_QUARANTINE", GO_TEST)

    def test_the_quarantine_step_never_blocks(self):
        body = job(CI, "go-race")
        m = re.search(r"- name: Go quarantined tests \(non-blocking\)\n(.*?)(?=\n      #|\n      - )", body, re.S)
        self.assertTrue(m)
        self.assertIn("continue-on-error: true", m.group(1))
        self.assertIn("GO_TEST_QUARANTINE: only", m.group(1))

    def test_an_invalid_entry_fails_lint_and_the_gate(self):
        self.assertIn("quarantine_check.py check", job(CI, "lint"))
        guards = {g["name"]: g for g in GATE["always_on_guards"]}
        self.assertEqual(guards["quarantine-valid"]["inputs"], [".delivery/quarantine.json"])

    def test_the_file_exists_and_is_valid(self):
        data = json.loads((ROOT / ".delivery" / "quarantine.json").read_text(encoding="utf-8"))
        self.assertIsInstance(data["tests"], list)


class GateSteps(unittest.TestCase):
    def test_windows_and_web_lint_steps_exist(self):
        steps = {s["name"]: s for s in GATE["steps"]}
        self.assertEqual(steps["lint-windows"]["env"], {"GOOS": "windows"})
        self.assertEqual(steps["lint-web"]["run"], "pnpm -r lint")
        self.assertEqual(GATE["local_gate"], "smoke")

    def test_the_pre_push_hook_lints_windows_and_web(self):
        hook = (ROOT / ".githooks" / "pre-push").read_text(encoding="utf-8")
        self.assertIn("GOOS=windows golangci-lint run ./...", hook)
        self.assertIn("pnpm -r lint", hook)
        self.assertIn("-short", hook)


if __name__ == "__main__":
    unittest.main()
