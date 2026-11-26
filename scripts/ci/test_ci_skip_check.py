import os
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import ci_skip_check as c  # noqa: E402

REPO = "o/r"


def fake_api(trees, runs):
    def get(path):
        if "/git/commits/" in path:
            sha = path.rsplit("/", 1)[1]
            return {"tree": {"sha": trees[sha]}}
        if "/actions/workflows/" in path:
            return {"workflow_runs": runs}
        raise AssertionError(path)
    return get


class ShouldSkip(unittest.TestCase):
    def test_pull_request_never_skips(self):
        skip, _ = c.should_skip("pull_request", REPO, "m", fake_api({}, []))
        self.assertFalse(skip)

    def pr_get(self, names):
        return lambda path: [{"filename": n} for n in names]

    def test_config_only_pull_request_skips(self):
        get = self.pr_get([".nvmrc", ".claude/settings.json", ".delivery/config.json"])
        self.assertTrue(c.should_skip("pull_request", REPO, "m", get, pr="7")[0])

    def test_code_in_pull_request_runs(self):
        get = self.pr_get([".nvmrc", "internal/x.go"])
        self.assertFalse(c.should_skip("pull_request", REPO, "m", get, pr="7")[0])

    def test_workflow_and_delivery_scripts_run(self):
        for n in (".github/workflows/ci.yml", ".delivery/gate-result.json", "package.json"):
            get = self.pr_get([".nvmrc", n])
            self.assertFalse(c.should_skip("pull_request", REPO, "m", get, pr="7")[0], n)

    def test_empty_pull_request_runs(self):
        self.assertFalse(c.should_skip("pull_request", REPO, "m", self.pr_get([]), pr="7")[0])

    def test_same_tree_skips(self):
        get = fake_api({"m": "T", "p1": "X", "p2": "T"},
                       [{"id": 1, "head_sha": "p1", "conclusion": "success"},
                        {"id": 2, "head_sha": "p2", "conclusion": "success"}])
        self.assertTrue(c.should_skip("push", REPO, "m", get)[0])

    def test_different_tree_runs(self):
        get = fake_api({"m": "T", "p1": "X"},
                       [{"id": 1, "head_sha": "p1", "conclusion": "success"}])
        self.assertFalse(c.should_skip("push", REPO, "m", get)[0])

    def test_no_runs_runs(self):
        self.assertFalse(c.should_skip("push", REPO, "m", fake_api({"m": "T"}, []))[0])

    def test_unsuccessful_run_ignored(self):
        get = fake_api({"m": "T", "p1": "T"},
                       [{"id": 1, "head_sha": "p1", "conclusion": "failure"}])
        self.assertFalse(c.should_skip("push", REPO, "m", get)[0])


class Main(unittest.TestCase):
    def run_main(self, env, api):
        with tempfile.TemporaryDirectory() as d:
            out = os.path.join(d, "out")
            env = dict(env, GITHUB_OUTPUT=out)
            with mock.patch.dict(os.environ, env, clear=True), \
                    mock.patch.object(c, "api_get", side_effect=api):
                self.assertEqual(c.main(), 0)
            with open(out, encoding="utf-8") as fh:
                return fh.read()

    ENV = {"GITHUB_EVENT_NAME": "push", "GITHUB_REPOSITORY": REPO,
           "GITHUB_SHA": "m", "GITHUB_TOKEN": "t"}

    def test_api_error_fails_open(self):
        self.assertEqual(self.run_main(self.ENV, OSError("boom")), "skip=false\n")

    def test_missing_env_fails_open(self):
        self.assertEqual(self.run_main({}, OSError("unused")), "skip=false\n")

    def test_skip_written(self):
        get = fake_api({"m": "T", "p": "T"},
                       [{"id": 1, "head_sha": "p", "conclusion": "success"}])
        self.assertEqual(self.run_main(self.ENV, lambda p, t: get(p)), "skip=true\n")


if __name__ == "__main__":
    unittest.main()
