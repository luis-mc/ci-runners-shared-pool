#!/usr/bin/env python3
"""Key and lookup for the stored Linux base image (CRSP323). Standard library only.

The `tools` stage of images/linux-base/Dockerfile is the slow part of the CI
`image` job. Main builds it and pushes it to a public ghcr.io package tagged
with a key over its inputs; a pull request whose inputs give the same key pulls
that image instead of building it.

  image_key.py key --arch amd64 [--extra NAME=VALUE ...]
      Print the key (and write `key=` to $GITHUB_OUTPUT).
  image_key.py lookup --repo ghcr.io/o/r/base-amd64 --key KEY
      Ask the registry whether REPO:KEY exists and was built by main. Writes
      hit=true|false, digest=, run_id= to $GITHUB_OUTPUT. Never fails: any error
      means hit=false and the job builds.

The key is a SHA-256 over: every file under images/ (path and content, the
text line endings folded to LF, so a Windows checkout gives the same key), the
Playwright pin of internal/runners/layers.go, the architecture and any --extra
pin, plus KEY_VERSION. The crsp binary that CI copies into images/linux-base is
not an input of the tools stage and is skipped. Order does not matter.
"""
import argparse
import hashlib
import json
import os
import re
import subprocess
import sys

KEY_VERSION = "1"
IMAGE_DIR = "images"
# Built by CI at run time, never in git; the tools stage does not depend on it.
SKIP = {"images/linux-base/crsp"}
PLAYWRIGHT_FILE = "internal/runners/layers.go"
PLAYWRIGHT_RE = re.compile(r'PlaywrightPin\s*=\s*"([^"]+)"')
# Labels the publishing job writes; the guard reads them back.
LABEL_REF = "org.crsp.built-by-ref"
LABEL_RUN = "org.crsp.run-id"
LABEL_INPUTS = "org.crsp.inputs-hash"
MAIN_REF = "refs/heads/main"


def normalise(data):
    """LF line endings for text; binary content (a NUL byte) is left alone."""
    if b"\0" in data:
        return data
    return data.replace(b"\r\n", b"\n")


def image_files(root):
    """Sorted (posix path, absolute path) of every file under images/."""
    out = []
    base = os.path.join(root, IMAGE_DIR)
    for dirpath, dirnames, filenames in os.walk(base):
        dirnames[:] = [d for d in dirnames if d not in ("__pycache__", ".git")]
        for name in filenames:
            full = os.path.join(dirpath, name)
            rel = os.path.relpath(full, root).replace(os.sep, "/")
            if rel in SKIP:
                continue
            out.append((rel, full))
    return sorted(out)


def playwright_pin(root):
    with open(os.path.join(root, PLAYWRIGHT_FILE), encoding="utf-8") as fh:
        m = PLAYWRIGHT_RE.search(fh.read())
    if not m:
        raise SystemExit("PlaywrightPin not found in " + PLAYWRIGHT_FILE)
    return m.group(1)


def compute_key(root, arch, extra=()):
    entries = []
    for rel, full in image_files(root):
        with open(full, "rb") as fh:
            digest = hashlib.sha256(normalise(fh.read())).hexdigest()
        entries.append("file %s %s" % (rel, digest))
    entries.append("playwright %s" % playwright_pin(root))
    entries.append("arch %s" % arch)
    for item in extra:
        entries.append("extra %s" % item)
    entries.append("version %s" % KEY_VERSION)
    h = hashlib.sha256()
    for line in sorted(entries):
        h.update(line.encode("utf-8") + b"\n")
    return h.hexdigest()


def labels_of(config):
    """Find the label map in an image config, as a bare config or a map of them."""
    if not isinstance(config, dict):
        return {}
    inner = config.get("config")
    if isinstance(inner, dict):
        return inner.get("Labels") or {}
    for value in config.values():
        found = labels_of(value)
        if found:
            return found
    return {}


def built_by_main(labels, key):
    """The guard: only an image a run on main pushed for this key counts."""
    run_id = labels.get(LABEL_RUN, "")
    ok = (
        labels.get(LABEL_REF) == MAIN_REF
        and labels.get(LABEL_INPUTS) == key
        and run_id.isdigit()
    )
    return ok, run_id


def run_text(cmd):
    return subprocess.run(
        cmd, check=True, capture_output=True, text=True, timeout=120
    ).stdout


def lookup(repo, key, run=run_text):
    """Return (hit, digest, run_id). Any error is a miss."""
    ref = "%s:%s" % (repo, key)
    try:
        base = ["docker", "buildx", "imagetools", "inspect", ref, "--format"]
        manifest = json.loads(run(base + ["{{json .Manifest}}"]))
        digest = manifest["digest"]
        config = json.loads(run(base + ["{{json .Image}}"]))
        ok, run_id = built_by_main(labels_of(config), key)
        if not ok:
            print("stored image %s is not marked as built by main: build" % ref)
            return False, "", ""
        return True, digest, run_id
    except Exception as exc:  # fail open: a build is always correct
        print("no stored image for %s (%s): build" % (ref, type(exc).__name__))
        return False, "", ""


def write_outputs(pairs):
    for k, v in pairs:
        print("%s=%s" % (k, v))
    path = os.environ.get("GITHUB_OUTPUT")
    if path:
        with open(path, "a", encoding="utf-8") as fh:
            for k, v in pairs:
                fh.write("%s=%s\n" % (k, v))


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    sub = ap.add_subparsers(dest="cmd", required=True)
    k = sub.add_parser("key")
    k.add_argument("--arch", required=True)
    k.add_argument("--extra", action="append", default=[])
    k.add_argument("--root", default=".")
    q = sub.add_parser("lookup")
    q.add_argument("--repo", required=True)
    q.add_argument("--key", required=True)
    args = ap.parse_args(argv)
    if args.cmd == "key":
        write_outputs([("key", compute_key(args.root, args.arch, args.extra))])
    else:
        hit, digest, run_id = lookup(args.repo, args.key)
        write_outputs(
            [("hit", "true" if hit else "false"), ("digest", digest), ("run_id", run_id)]
        )
    return 0


if __name__ == "__main__":
    sys.exit(main())
