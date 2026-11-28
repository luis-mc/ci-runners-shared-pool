"""CRSP324: the CI area map (scripts/delivery/ci-areas.json) switches jobs the way the workflow expects."""
import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "scripts", "delivery"))
import area_select as a  # noqa: E402

MAP = a.load_map(__import__("pathlib").Path(ROOT) / "scripts" / "delivery" / "ci-areas.json")
EVERY = ["lint", "types", "go-race", "web-tests", "test-os", "go_win_lint", "build", "image", "e2e-shards"]
WEB = ["lint", "types", "web-tests", "build", "image", "e2e-shards"]


def pick(*paths, force_all=False):
    return a.select(list(paths), MAP, force_all)


def on(res):
    return sorted(j for j, v in res["jobs"].items() if v)


class AreaMap(unittest.TestCase):
    def test_every_job_is_listed(self):
        self.assertEqual(sorted(a.all_jobs(MAP)), sorted(EVERY))

    def test_go_file_runs_go_and_windows(self):
        r = pick("apps/controller/internal/agent/x.go")
        self.assertEqual(r["areas"], ["go"])
        self.assertEqual(on(r), sorted(EVERY))

    def test_windows_only_go_file_keeps_windows_lint(self):
        self.assertTrue(pick("apps/controller/internal/cpucap/cap_windows.go")["jobs"]["go_win_lint"])

    def test_web_change_skips_go_and_os_jobs(self):
        r = pick("apps/controller-console/src/a.tsx")
        self.assertEqual(on(r), sorted(WEB))
        for j in ("go-race", "test-os", "go_win_lint"):
            self.assertFalse(r["jobs"][j])

    def test_e2e_only(self):
        self.assertEqual(on(pick("e2e/a.spec.ts")), ["build", "e2e-shards", "lint", "types"])

    def test_windows_file_without_go(self):
        self.assertEqual(on(pick("installer/a.ps1")), ["build", "go-race", "go_win_lint", "lint", "test-os", "types", "web-tests"])
        self.assertEqual(on(pick("apps/controller/cmd/crsp/rsrc_windows_amd64.syso")), sorted(EVERY))  # under apps/controller/cmd/: go area too
        self.assertEqual(on(pick("scripts/install/x.ps1")), ["build", "go_win_lint", "lint", "test-os"])

    def test_shared_files_run_everything(self):
        for p in ("go.mod", "go.sum", "pnpm-lock.yaml", "package.json", ".github/workflows/ci.yml",
                  "scripts/delivery/ci-areas.json", "scripts/ci/go-test.sh"):
            r = pick(p)
            self.assertEqual(r["mode"], "all", p)
            self.assertEqual(on(r), sorted(EVERY), p)

    def test_unknown_path_runs_everything(self):
        for p in ("brand-new-dir/file.txt", "scripts/lifecycle/notes.txt"):
            self.assertEqual(pick(p)["mode"], "all", p)

    def test_mixed_change_unites_areas(self):
        r = pick("apps/controller-console/src/a.tsx", "apps/controller/internal/x/y.go")
        self.assertEqual(on(r), sorted(EVERY))

    def test_docs_only_runs_only_lint(self):
        r = pick("docs/a.md", ".claude/x.json")
        self.assertEqual(r["mode"], "none")
        self.assertEqual(on(r), ["lint"])

    def test_lint_config_only_runs_lint(self):
        self.assertEqual(on(pick(".golangci.yml")), ["lint"])

    def test_every_tracked_file_is_mapped(self):
        """DS287 (R5.94): the pull request check `area_select.py --unmapped --check` passes."""
        import subprocess
        files = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, check=True).stdout.decode("utf-8", "replace")
        self.assertEqual(a.unmapped([f for f in files.split("\0") if f], MAP), [])

    def test_no_paths_and_forced_run_everything(self):
        self.assertEqual(on(a.select([], MAP)), sorted(EVERY))
        self.assertEqual(on(pick("docs/a.md", force_all=True)), sorted(EVERY))


def job_text(name):
    """The text of one top-level job of ci.yml (from `  name:` to the next job)."""
    text = open(os.path.join(ROOT, ".github", "workflows", "ci.yml"), encoding="utf-8").read()
    jobs = text.split("\njobs:\n", 1)[1]
    parts = __import__("re").split(r"\n(?=  [a-z0-9-]+:\n)", jobs)
    return next(p for p in parts if p.lstrip().startswith(name + ":"))


class LifecycleStaysWhole(unittest.TestCase):
    """DL-112: a release pull request needs every lifecycle target; no path list may turn a lane off."""

    def test_lifecycle_job_is_not_gated_by_the_area_plan(self):
        job = job_text("lifecycle")
        self.assertIn("needs.decide.outputs.release == 'true'", job)
        for out in ("areas", "go-race", "web-tests", "test-os", "go_win_lint", "build", "e2e-shards"):
            self.assertNotIn("outputs." + out, job)

    def test_release_pull_request_plans_everything(self):
        step = job_text("decide").split("- id: areas", 1)[1]
        self.assertIn('[ "$RELEASE" = "true" ]', step)
        self.assertIn("args+=(--all)", step)

    def test_release_pattern_matches_the_delivery_config(self):
        import json
        cfg = json.load(open(os.path.join(ROOT, ".delivery", "config.json"), encoding="utf-8"))
        self.assertEqual(cfg["release_branch_re"], "^release/")
        self.assertIn("=~ ^release/", job_text("decide"))


class CarryWiring(unittest.TestCase):
    """CRSP349 (DS287): every job of the carry plan is a workflow job and the aggregates read the carry."""

    TEXT = open(os.path.join(ROOT, ".github", "workflows", "ci.yml"), encoding="utf-8").read()

    def test_map_jobs_are_workflow_jobs(self):
        keys = set(a.parse_workflow_jobs(self.TEXT))
        for job in a.all_jobs(MAP):
            if job != "go_win_lint":  # a step of lint, not a job
                self.assertIn(job, keys)

    def test_work_jobs_wait_for_the_carry_plan(self):
        for job in ("lint", "types", "go-race", "web-tests", "test-os", "build", "image", "e2e-shards"):
            text = job_text(job)
            self.assertIn("carry", text.split("steps:")[0].split("uses:")[0], job)
            self.assertIn("needs.carry.outputs.plan", text, job)

    def test_aggregates_count_a_carried_skip(self):
        for job in ("tests-result", "verify", "e2e"):
            self.assertIn("needs.carry.outputs.carried", job_text(job), job)

    def test_carry_ok_uses_carry_verify(self):
        self.assertIn("carry_verify.py", job_text("carry-ok"))


if __name__ == "__main__":
    unittest.main()
