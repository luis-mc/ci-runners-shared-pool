import os
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import image_scope as s  # noqa: E402

REPO = "o/r"


def files_api(pages):
    """pages: list of lists of file dicts, one per page."""
    def get(path):
        assert "/pulls/7/files" in path, path
        page = int(path.rsplit("page=", 1)[1])
        return pages[page - 1] if page <= len(pages) else []
    return get


def named(*names):
    return [{"filename": n} for n in names]


class ShouldBuild(unittest.TestCase):
    def no_api(self, path):
        raise AssertionError("no API call expected: " + path)

    def test_push_builds(self):
        self.assertTrue(s.should_build("push", REPO, "", "", self.no_api)[0])

    def test_manual_run_builds(self):
        self.assertTrue(s.should_build("workflow_dispatch", REPO, "", "", self.no_api)[0])

    def test_release_pull_request_builds(self):
        self.assertTrue(s.should_build("pull_request", REPO, "7", "CRSP184b-integration", self.no_api)[0])

    def test_release_n_pull_request_builds(self):
        self.assertTrue(s.should_build("pull_request", REPO, "7", "CRSP222-release-2", self.no_api)[0])

    def test_release_word_alone_is_not_a_release(self):
        get = files_api([named("docs/a.md")])
        self.assertFalse(s.should_build("pull_request", REPO, "7", "OPS1-release-notes", get)[0])

    def test_unrelated_branch_skips(self):
        get = files_api([named("apps/controller/internal/x.go", "docs/a.md")])
        self.assertFalse(s.should_build("pull_request", REPO, "7", "OPS1-x", get)[0])

    def test_images_change_builds(self):
        get = files_api([named("apps/controller/internal/x.go", "images/layers/kvm/Dockerfile")])
        self.assertTrue(s.should_build("pull_request", REPO, "7", "OPS1-x", get)[0])

    def test_workflow_change_builds(self):
        for f in (".github/workflows/ci.yml", ".github/workflows/build.yml"):
            get = files_api([named(f)])
            self.assertTrue(s.should_build("pull_request", REPO, "7", "OPS1-x", get)[0], f)

    def test_other_workflow_skips(self):
        get = files_api([named(".github/workflows/package.yml", "imagesx/a")])
        self.assertFalse(s.should_build("pull_request", REPO, "7", "OPS1-x", get)[0])

    def test_rename_out_of_images_builds(self):
        get = files_api([[{"filename": "tmp/Dockerfile", "previous_filename": "images/linux-base/Dockerfile"}]])
        self.assertTrue(s.should_build("pull_request", REPO, "7", "OPS1-x", get)[0])

    def test_second_page_is_read(self):
        page1 = named(*["a/%d" % i for i in range(100)])
        page2 = named("images/linux-base/Dockerfile")
        self.assertTrue(s.should_build("pull_request", REPO, "7", "OPS1-x", files_api([page1, page2]))[0])


class Main(unittest.TestCase):
    def run_main(self, env):
        with tempfile.NamedTemporaryFile("r+", suffix=".out") as out:
            env = dict(env, GITHUB_OUTPUT=out.name)
            with mock.patch.dict(os.environ, env, clear=True):
                s.main()
            out.seek(0)
            return out.read().strip()

    def test_api_error_builds(self):
        env = {"GITHUB_EVENT_NAME": "pull_request", "GITHUB_REPOSITORY": REPO,
               "PR_NUMBER": "7", "PR_HEAD_REF": "OPS1-x", "GITHUB_TOKEN": "t"}
        with mock.patch.object(s, "api_get", side_effect=OSError("down")):
            self.assertEqual(self.run_main(env), "image=true")

    def test_push_output(self):
        self.assertEqual(self.run_main({"GITHUB_EVENT_NAME": "push"}), "image=true")


if __name__ == "__main__":
    unittest.main()
