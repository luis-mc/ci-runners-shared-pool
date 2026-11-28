#!/usr/bin/env python3
"""CRSP274: release notes from the change notes (DOCSPEC: docs/features/<feature>/changes/<task>.md and
docs/<area>/changes/<doc>/<task>.md). A change note is a file with a front matter (change, task) and one text.

  release_notes.py [--root .] [--release N] [--out FILE]

Prints "## What's new" with one bullet per change note, grouped by document, major changes first. The notes that are
still in the tree are the ones the release fold has not written into a version table yet, so they are the notes of
the release being cut. --release N keeps a note unless the project record (docs/project/registry.json) puts its task
in another release (the same rule as docs_fold.py). Prints nothing when there is no note.
"""
import argparse
import json
import re
import sys
from pathlib import Path

ORDER = {"major": 0, "minor": 1, "patch": 2}


def front(text):
    m = re.match(r"^---\n(.*?)\n---\n?(.*)$", text, re.S)
    if not m:
        return {}, text.strip()
    meta = {}
    for line in m.group(1).splitlines():
        k, _, v = line.partition(":")
        meta[k.strip()] = v.strip()
    return meta, m.group(2).strip()


def title_of(doc):
    try:
        for line in doc.read_text(encoding="utf-8").splitlines()[:12]:
            if line.startswith("title:"):
                return line.partition(":")[2].strip().strip("\"'")
    except OSError:
        pass
    return doc.parent.name if doc.name == "spec.md" else doc.stem


def task_releases(root):
    """task id -> release number (own `release`, or the one of the task it rides); None without a record."""
    try:
        data = json.loads((root / "docs/project/registry.json").read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    tasks = data.get("tasks") if isinstance(data, dict) else None
    if not isinstance(tasks, list):
        return None
    by_id = {str(t["id"]): t for t in tasks if isinstance(t, dict) and t.get("id")}
    out = {}
    for tid, t in by_id.items():
        seen, cur = set(), t
        while cur is not None:
            n = cur.get("release")
            if isinstance(n, int) and not isinstance(n, bool) and n > 0:
                out[tid] = n
                break
            seen.add(str(cur.get("id")))
            nxt = by_id.get(str(cur.get("rides") or ""))
            cur = nxt if nxt is not None and str(nxt.get("id")) not in seen else None
    return out


def collect(root):
    notes = []
    for p in sorted((root / "docs/features").glob("*/changes/*.md")):
        notes.append((p, p.parent.parent / "spec.md"))
    for p in sorted((root / "docs").glob("*/changes/*/*.md")):
        notes.append((p, p.parent.parent.parent / (p.parent.name + ".md")))
    return notes


def build(root, release=None):
    known = task_releases(root) if release else None
    groups = {}
    for path, doc in collect(root):
        meta, text = front(path.read_text(encoding="utf-8"))
        if not text:
            continue
        task = meta.get("task", path.stem)
        if release and known is not None and known.get(task) not in (None, release):
            continue
        groups.setdefault(title_of(doc), []).append((ORDER.get(meta.get("change", "minor"), 1), task, " ".join(text.split())))
    if not groups:
        return ""
    lines = ["## What's new", ""]
    for title in sorted(groups):
        lines.append("**%s**" % title)
        lines.append("")
        for _, task, text in sorted(groups[title]):
            lines.append("- %s (%s)" % (text, task))
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    ap.add_argument("--release", type=int)
    ap.add_argument("--out")
    a = ap.parse_args(argv)
    text = build(Path(a.root), a.release)
    if a.out:
        Path(a.out).write_text(text, encoding="utf-8")
    else:
        sys.stdout.write(text)


if __name__ == "__main__":
    main()
