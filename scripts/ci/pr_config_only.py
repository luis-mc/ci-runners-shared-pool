#!/usr/bin/env python3
"""Config-only pull request skip (DS261, S15). Standard library only.

A pull request whose every changed file is repo config skips the suite through the `decide` job, so ci.yml still
starts and ends green (the delivery watcher needs that run). The push to the default branch is decided by the
shipped scripts/delivery/ci_skip_check.py (CRSP375, S16); this script only handles pull requests and never skips
a push. It never fails the job: on any error the suite runs.

Env: GITHUB_EVENT_NAME, GITHUB_REPOSITORY, GITHUB_TOKEN, PR_NUMBER, GITHUB_OUTPUT (optional).
Writes `skip=true|false` to $GITHUB_OUTPUT.
"""
import fnmatch
import json
import os
import sys
import urllib.request

API = "https://api.github.com"

# Product code, workflows other than dev-standards, images and lockfiles are never in this list.
CONFIG_ONLY = (
    ".delivery/config.json",
    ".claude/*",
    ".nvmrc",
    ".gitignore",
    ".github/workflows/dev-standards.yml",
)


def api_get(path, token):
    req = urllib.request.Request(
        API + path,
        headers={
            "Accept": "application/vnd.github+json",
            "Authorization": "Bearer " + token,
            "X-GitHub-Api-Version": "2022-11-28",
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def is_config_only(files):
    return bool(files) and all(
        any(fnmatch.fnmatchcase(f, pat) for pat in CONFIG_ONLY) for f in files
    )


def pr_files(repo, number, get):
    files, page = [], 1
    while page <= 10:
        batch = get("/repos/%s/pulls/%s/files?per_page=100&page=%d" % (repo, number, page))
        files += [f["filename"] for f in batch]
        if len(batch) < 100:
            return files
        page += 1
    return None  # too many files: run the suite



def should_skip(event, repo, get, pr=None):
    """Return (skip, reason). `get` maps an API path to parsed JSON."""
    if event != "pull_request" or not pr:
        return False, "event %s is not a pull request" % event
    files = pr_files(repo, pr, get)
    if files is not None and is_config_only(files):
        return True, "config-only pull request (%d files)" % len(files)
    return False, "pull request changes more than repo config"


def write_output(skip):
    line = "skip=%s\n" % ("true" if skip else "false")
    path = os.environ.get("GITHUB_OUTPUT")
    if path:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(line)
    sys.stdout.write(line)


def main():
    env = os.environ
    try:
        token = env["GITHUB_TOKEN"]
        skip, reason = should_skip(
            env.get("GITHUB_EVENT_NAME", ""),
            env["GITHUB_REPOSITORY"],
            lambda p: api_get(p, token),
            env.get("PR_NUMBER") or None,
        )
    except Exception as exc:  # fail open: the suite runs
        skip, reason = False, "check failed, running the suite: %s" % exc
    print("pr_config_only: " + reason, file=sys.stderr)
    write_output(skip)
    return 0


if __name__ == "__main__":
    sys.exit(main())
