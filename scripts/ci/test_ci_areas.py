"""CRSP324: the CI area map (scripts/delivery/ci-areas.json) switches jobs the way the workflow expects."""
import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
sys.path.insert(0, os.path.join(ROOT, "scripts", "delivery"))
import area_select as a  # noqa: E402

MAP = a.load_map(__import__("pathlib").Path(ROOT) / "scripts" / "delivery" / "ci-areas.json")
EVERY = ["go_race", "web_tests", "test_os", "go_win_lint", "build", "e2e"]


def pick(*paths, force_all=False):
    return a.select(list(paths), MAP, force_all)


def on(res):
    return sorted(j for j, v in res["jobs"].items() if v)


class AreaMap(unittest.TestCase):
    def test_every_job_is_listed(self):
        self.assertEqual(sorted(a.all_jobs(MAP)), sorted(EVERY))

    def test_go_file_runs_go_and_windows(self):
        r = pick("internal/agent/x.go")
        self.assertEqual(r["areas"], ["go"])
        self.assertEqual(on(r), sorted(EVERY))

    def test_windows_only_go_file_keeps_windows_lint(self):
        self.assertTrue(pick("internal/cpucap/cap_windows.go")["jobs"]["go_win_lint"])

    def test_web_change_skips_go_and_os_jobs(self):
        r = pick("apps/web/src/a.tsx")
        self.assertEqual(on(r), ["build", "e2e", "web_tests"])
        for j in ("go_race", "test_os", "go_win_lint"):
            self.assertFalse(r["jobs"][j])

    def test_e2e_only(self):
        self.assertEqual(on(pick("e2e/a.spec.ts")), ["build", "e2e"])

    def test_windows_file_without_go(self):
        self.assertEqual(on(pick("installer/a.ps1")), ["build", "go_race", "go_win_lint", "test_os", "web_tests"])
        self.assertEqual(on(pick("cmd/crsp/rsrc_windows_amd64.syso")), sorted(EVERY))  # under cmd/: go area too
        self.assertEqual(on(pick("scripts/install/x.ps1")), ["build", "go_win_lint", "test_os"])

    def test_shared_files_run_everything(self):
        for p in ("go.mod", "go.sum", "pnpm-lock.yaml", "package.json", ".github/workflows/ci.yml",
                  "scripts/delivery/ci-areas.json", "scripts/ci/go-test.sh"):
            r = pick(p)
            self.assertEqual(r["mode"], "all", p)
            self.assertEqual(on(r), sorted(EVERY), p)

    def test_unknown_path_runs_everything(self):
        for p in ("brand-new-dir/file.txt", ".nvmrc", "tsconfig.json", "scripts/lifecycle/notes.txt"):
            self.assertEqual(pick(p)["mode"], "all", p)

    def test_mixed_change_unites_areas(self):
        r = pick("apps/web/src/a.tsx", "internal/x/y.go")
        self.assertEqual(on(r), sorted(EVERY))

    def test_docs_only_runs_no_job(self):
        r = pick("docs/a.md", ".claude/x.json")
        self.assertEqual(r["mode"], "none")
        self.assertEqual(on(r), [])

    def test_lint_config_only_runs_no_test_job(self):
        self.assertEqual(on(pick(".golangci.yml")), [])

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
        for out in ("areas", "go_race", "web_tests", "test_os", "go_win_lint", "build", "e2e"):
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


if __name__ == "__main__":
    unittest.main()
