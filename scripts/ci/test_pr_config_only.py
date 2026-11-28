import os
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import pr_config_only as c  # noqa: E402

REPO = "o/r"


def pr_get(names):
    return lambda path: [{"filename": n} for n in names]


class ShouldSkip(unittest.TestCase):
    def test_config_only_pull_request_skips(self):
        get = pr_get([".nvmrc", ".claude/settings.json", ".delivery/config.json"])
        self.assertTrue(c.should_skip("pull_request", REPO, get, pr="7")[0])

    def test_code_in_pull_request_runs(self):
        get = pr_get([".nvmrc", "apps/controller/internal/x.go"])
        self.assertFalse(c.should_skip("pull_request", REPO, get, pr="7")[0])

    def test_workflow_and_delivery_scripts_run(self):
        for n in (".github/workflows/ci.yml", ".delivery/gate-result.json", "package.json"):
            self.assertFalse(c.should_skip("pull_request", REPO, pr_get([".nvmrc", n]), pr="7")[0], n)

    def test_empty_pull_request_runs(self):
        self.assertFalse(c.should_skip("pull_request", REPO, pr_get([]), pr="7")[0])

    def test_a_push_never_skips_here(self):
        # the push to the default branch is decided by scripts/delivery/ci_skip_check.py
        self.assertFalse(c.should_skip("push", REPO, pr_get([".nvmrc"]))[0])


class Main(unittest.TestCase):
    def run_main(self, env, api):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "out")
            with mock.patch.dict(os.environ, dict(env, GITHUB_OUTPUT=out), clear=True), \
                    mock.patch.object(c, "api_get", side_effect=api):
                self.assertEqual(c.main(), 0)
            with open(out, encoding="utf-8") as fh:
                return fh.read()

    ENV = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REPOSITORY": REPO, "GITHUB_TOKEN": "t", "PR_NUMBER": "7"}

    def test_api_error_fails_open(self):
        self.assertEqual(self.run_main(self.ENV, OSError("boom")), "skip=false\n")

    def test_missing_env_fails_open(self):
        self.assertEqual(self.run_main({}, OSError("unused")), "skip=false\n")

    def test_skip_written(self):
        self.assertEqual(self.run_main(self.ENV, lambda p, t: [{"filename": ".nvmrc"}]), "skip=true\n")


if __name__ == "__main__":
    unittest.main()
