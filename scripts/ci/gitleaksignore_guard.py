#!/usr/bin/env python3
"""Fail when a change adds any line to .gitleaksignore (CRSP341).

    gitleaksignore_guard.py [--base REF] [--head REF] [--file PATH]

The secret scan is never allowlisted: an agent that cannot pass it changes the code.
Removing lines is fine. Compares HEAD with the merge base of BASE (default
origin/main). Exit 0 pass, 1 a line was added, 2 usage or git error.
"""
from __future__ import annotations

import argparse
import subprocess
import sys

IGNORE_FILE = ".gitleaksignore"


def added_lines(diff: str) -> list[str]:
    """Lines a unified diff adds (not the +++ file header)."""
    return [ln[1:] for ln in diff.splitlines() if ln.startswith("+") and not ln.startswith("+++")]


def check(diff: str, name: str = IGNORE_FILE) -> tuple[bool, str]:
    added = added_lines(diff)
    if not added:
        return True, "%s: no line added" % name
    shown = "\n".join("    + " + ln for ln in added[:20])
    return False, (
        "FAIL %s: %d line(s) added. Never allowlist the secret scan.\n%s\n"
        "Build planted fake secrets at run time (concatenate or generate) and change the code, "
        "not %s or .gitleaks.toml (AGENTS.md, Secret scan)." % (name, len(added), shown, name)
    )


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="gitleaksignore_guard.py", description=__doc__.split("\n\n")[0])
    ap.add_argument("--base", default="origin/main")
    ap.add_argument("--head", default="HEAD")
    ap.add_argument("--file", default=IGNORE_FILE)
    a = ap.parse_args(argv)
    r = subprocess.run(
        ["git", "diff", "--unified=0", "--no-color", "%s...%s" % (a.base, a.head), "--", a.file],
        capture_output=True, text=True, encoding="utf-8", errors="replace",
    )
    if r.returncode != 0:
        print("gitleaksignore_guard: git diff failed: %s" % r.stderr.strip(), file=sys.stderr)
        return 2
    ok, msg = check(r.stdout, a.file)
    print(msg)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
