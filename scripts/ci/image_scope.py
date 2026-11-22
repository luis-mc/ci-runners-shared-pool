#!/usr/bin/env python3
"""Decide whether the CI `image` job runs (OPS41). Standard library only.

The base image build is slow, so it runs on a release pull request (head
branch ends in -integration or -release-N), on a push to the default branch and on a manual
run. On any other pull request it runs only when the change touches images/**
or the build or ci workflow files. An API error means image=true: the job runs.
The script never fails the job.

Env: GITHUB_EVENT_NAME, GITHUB_REPOSITORY, GITHUB_TOKEN, PR_NUMBER, PR_HEAD_REF,
GITHUB_OUTPUT (optional).
"""
import json
import os
import re
import sys
import urllib.request

API = "https://api.github.com"
WATCHED_FILES = (".github/workflows/ci.yml", ".github/workflows/build.yml")
WATCHED_PREFIX = "images/"
MAX_PAGES = 30


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


def touches_image(paths):
    return any(p.startswith(WATCHED_PREFIX) or p in WATCHED_FILES for p in paths)


def changed_files(repo, number, get):
    names = []
    for page in range(1, MAX_PAGES + 1):
        data = get("/repos/%s/pulls/%s/files?per_page=100&page=%d" % (repo, number, page))
        names.extend(f["filename"] for f in data)
        # A renamed file also counts under its old name.
        names.extend(f["previous_filename"] for f in data if f.get("previous_filename"))
        if len(data) < 100:
            break
    return names


def is_release_branch(head_ref):
    """A branch named *-integration or *-release-N is a release (CRSP222)."""
    return head_ref.endswith("-integration") or re.search(r"-release-[0-9]+$", head_ref) is not None


def should_build(event, repo, number, head_ref, get):
    """Return (build, reason). `get` maps an API path to parsed JSON."""
    if event != "pull_request":
        return True, "event %s always builds the image" % event
    if is_release_branch(head_ref):
        return True, "release pull request (%s)" % head_ref
    if touches_image(changed_files(repo, number, get)):
        return True, "the change touches images/ or the build or ci workflow"
    return False, "no change in images/ or the build or ci workflow"


def write_output(build):
    line = "image=%s\n" % ("true" if build else "false")
    path = os.environ.get("GITHUB_OUTPUT")
    if path:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(line)
    sys.stdout.write(line)


def main():
    env = os.environ
    try:
        token = env.get("GITHUB_TOKEN", "")
        build, reason = should_build(
            env.get("GITHUB_EVENT_NAME", ""),
            env.get("GITHUB_REPOSITORY", ""),
            env.get("PR_NUMBER", ""),
            env.get("PR_HEAD_REF", ""),
            lambda p: api_get(p, token),
        )
    except Exception as exc:  # fail open: the image builds
        build, reason = True, "check failed, building the image: %s" % exc
    print("image_scope: " + reason, file=sys.stderr)
    write_output(build)
    return 0


if __name__ == "__main__":
    sys.exit(main())
