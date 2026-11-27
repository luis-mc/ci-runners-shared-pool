"""CRSP341: a pull request that adds a line to .gitleaksignore fails; removing lines passes."""
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent))
import gitleaksignore_guard as g  # noqa: E402

ADD = "--- a/.gitleaksignore\n+++ b/.gitleaksignore\n@@ -1,0 +2 @@\n+abc123:file.go:generic-api-key:10\n"
REMOVE = "--- a/.gitleaksignore\n+++ b/.gitleaksignore\n@@ -2 +1,0 @@\n-abc123:file.go:generic-api-key:10\n"


def test_added_line_fails():
    ok, msg = g.check(ADD)
    assert not ok and "FAIL" in msg


def test_removed_line_passes():
    assert g.check(REMOVE)[0]


def test_empty_diff_passes():
    assert g.check("")[0]


def _git(cwd, *a):
    subprocess.run(["git", "-c", "user.email=t@t", "-c", "user.name=t", *a], cwd=cwd, check=True, capture_output=True)


def test_end_to_end(tmp_path, monkeypatch):
    _git(tmp_path, "init", "-q", "-b", "main")
    f = tmp_path / ".gitleaksignore"
    f.write_text("one\ntwo\n")
    _git(tmp_path, "add", "-A")
    _git(tmp_path, "commit", "-qm", "base")
    _git(tmp_path, "checkout", "-qb", "rm")
    f.write_text("one\n")
    _git(tmp_path, "commit", "-qam", "remove")
    monkeypatch.chdir(tmp_path)
    assert g.main(["--base", "main"]) == 0
    _git(tmp_path, "checkout", "-qb", "add")
    f.write_text("one\nthree\n")
    _git(tmp_path, "commit", "-qam", "add")
    assert g.main(["--base", "main"]) == 1
