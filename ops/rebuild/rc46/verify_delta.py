#!/usr/bin/env python3
"""Verify the pinned ancestry and report explicitly owned source deltas.

This is a source-provenance gate, not a business-equivalence or test-pass claim.
Run from any directory. Include uncommitted tracked edits with --worktree.
"""

import argparse
import json
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[3]


def git(*args):
    return subprocess.check_output(["git", "-C", str(ROOT), *args])


def tracked_tree(ref):
    entries = {}
    for row in git("ls-tree", "-rz", ref).split(b"\0"):
        if not row:
            continue
        metadata, path = row.split(b"\t", 1)
        mode, kind, blob = metadata.decode().split(" ")
        entries[path.decode()] = {"mode": mode, "kind": kind, "blob": blob}
    return entries


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--worktree", action="store_true")
    args = parser.parse_args()
    manifest_path = "ops/rebuild/rc46/manifest.json"
    if args.worktree:
        manifest = json.loads((ROOT / manifest_path).read_text())
    else:
        manifest = json.loads(git("show", "HEAD:" + manifest_path))
    upstream = manifest["upstream"]
    base = upstream["commit"]
    base_tree = git("rev-parse", base + "^{tree}").decode().strip()
    if base_tree != upstream["tree"]:
        raise SystemExit("Pinned upstream tree mismatch")
    subprocess.run(["git", "-C", str(ROOT), "merge-base", "--is-ancestor", base, "HEAD"], check=True)
    original = tracked_tree(base)
    if len(original) != upstream["tracked_files"]:
        raise SystemExit("Pinned upstream tracked-file count mismatch")
    stages = manifest["stages"]
    owners = {}
    errors = []
    stage_ids = set()
    for stage in stages:
        if stage["id"] in stage_ids:
            errors.append("Duplicate stage id: " + stage["id"])
        stage_ids.add(stage["id"])
        for kind in ("upstream_paths", "custom_paths"):
            for path in stage[kind]:
                if path in owners:
                    errors.append("Path has multiple stage owners: " + path)
                owners[path] = {"stage": stage["id"], "kind": kind}
                if (path in original) != (kind == "upstream_paths"):
                    errors.append("Incorrect upstream/custom classification: " + path)
    # A moved upstream file remains an upstream deletion plus a custom addition;
    # rename detection must not hide the source path from maintenance ownership.
    diff_args = ("diff", "--no-renames", "--name-only", "-z", base)
    if not args.worktree:
        diff_args += ("HEAD",)
    changed = {p.decode() for p in git(*diff_args).split(b"\0") if p}
    if args.worktree:
        changed.update(p.decode() for p in git("ls-files", "--others", "--exclude-standard", "-z").split(b"\0") if p)
    for path in sorted(changed):
        if path not in owners:
            errors.append("Unregistered source delta: " + path)
    result = {
        "base_commit": base,
        "base_tree": base_tree,
        "head": git("rev-parse", "HEAD").decode().strip(),
        "mode": "worktree" if args.worktree else "committed",
        "upstream_modified_or_removed": sorted(changed.intersection(original)),
        "custom_added": sorted(changed.difference(original)),
        "stage_ownership": {p: owners[p] for p in sorted(changed) if p in owners},
        "errors": errors,
        "scope": "Source provenance only; no semantic-equivalence or runtime-acceptance claim",
    }
    print(json.dumps(result, indent=2))
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
