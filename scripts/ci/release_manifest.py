#!/usr/bin/env python3
"""OPS69: build the update manifest of a release (the format of delivery lifecycle CONTRACT.md).

  release_manifest.py --version 0.1.22 --channel stable --dist dist --repo owner/name --tag v0.1.22 \
      [--now EPOCH] [--expires-days 180] [--required] --out dist/manifest.json

The file is the canonical manifest: JSON with sorted keys and the separators "," and ":", no spaces and no
final newline, so a signature over the file's bytes is a signature over the canonical manifest.
Keys: product, channel, version, required, expires_at, url, sha256 (the contract), and assets: one
{url, sha256} per release package, keyed <os>-<arch> (win-x64, win-arm64, linux-x64, ...), so an updater on
another target finds its own package. url and sha256 are the Windows x64 MSI, the contract's default package.
"""
import argparse
import hashlib
import json
import re
import sys
import time
from pathlib import Path

PRODUCT = "crsp"
NAME = re.compile(r"^Crsp-(?P<version>.+)-(?P<os>win|linux|macos)-(?P<arch>x64|arm64)\.(?P<ext>msi|deb|rpm|pkg|tar\.gz|zip)$")
DEFAULT = "win-x64"


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def canonical(manifest):
    return json.dumps(manifest, sort_keys=True, separators=(",", ":"), ensure_ascii=True).encode()


def build(version, channel, dist, repo, tag, now, expires_days, required):
    base = "https://github.com/%s/releases/download/%s/" % (repo, tag)
    assets = {}
    for p in sorted(Path(dist).iterdir()):
        m = NAME.match(p.name)
        if not m:
            continue
        if m.group("version") != version:
            raise SystemExit("%s does not carry version %s" % (p.name, version))
        if m.group("ext") != "msi":
            continue  # the updater installs the Windows MSI; the other packages are listed in SHA256SUMS
        assets["%s-%s" % (m.group("os"), m.group("arch"))] = {"url": base + p.name, "sha256": sha256(p)}
    if DEFAULT not in assets:
        raise SystemExit("no Crsp-%s-win-x64.msi in %s" % (version, dist))
    return {
        "product": PRODUCT,
        "channel": channel,
        "version": version,
        "required": bool(required),
        "expires_at": int(now) + int(expires_days) * 86400,
        "url": assets[DEFAULT]["url"],
        "sha256": assets[DEFAULT]["sha256"],
        "assets": assets,
    }


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--version", required=True)
    ap.add_argument("--channel", required=True, choices=["stable", "edge"])
    ap.add_argument("--dist", required=True)
    ap.add_argument("--repo", required=True)
    ap.add_argument("--tag", required=True)
    ap.add_argument("--now", type=int, default=None)
    ap.add_argument("--expires-days", type=int, default=180)
    ap.add_argument("--required", action="store_true")
    ap.add_argument("--out", required=True)
    a = ap.parse_args(argv)
    m = build(a.version.lstrip("v"), a.channel, a.dist, a.repo, a.tag,
              a.now if a.now is not None else time.time(), a.expires_days, a.required)
    Path(a.out).write_bytes(canonical(m))
    print("manifest %s %s: %d package(s)" % (m["channel"], m["version"], len(m["assets"])))
    return 0


if __name__ == "__main__":
    sys.exit(main())
