#!/usr/bin/env python3
"""Counts lines of code (non-blank, non-comment) in the monorepo, per component and kind.

Usage: loc.py [root] [--json out.json]
"""
import json
import os
import re
import sys

EXTS = {".go": "go", ".sol": "solidity", ".rs": "rust"}
SKIP_DIRS = {"node_modules", "lib", "forge-artifacts", "artifacts", "cache", "target", ".git", "testdata", "snapshots", "bin", ".devnet"}
COMMENT = re.compile(r"/\*.*?\*/", re.S)


def sloc(path):
    try:
        text = open(path, encoding="utf-8", errors="ignore").read()
    except OSError:
        return 0
    text = COMMENT.sub("", text)
    return sum(1 for line in text.splitlines() if line.strip() and not line.strip().startswith("//"))


def is_test(rel):
    return rel.endswith("_test.go") or rel.endswith(".t.sol") or "/test/" in f"/{rel}" or "/tests/" in f"/{rel}"


def component(rel):
    parts = rel.split("/")
    if parts[0] == "packages" and len(parts) > 2:
        if parts[1] == "contracts-bedrock" and len(parts) > 3:
            return "/".join(parts[:4]) if parts[2] in ("src", "interfaces", "scripts", "test") else "/".join(parts[:3])
        return "/".join(parts[:2])
    if parts[0] == "rust" and len(parts) > 2:
        if parts[1] == "kona" and len(parts) > 4 and parts[2] in ("crates", "bin", "sp1"):
            return "/".join(parts[:4 if parts[2] != "crates" else 5])
        return "/".join(parts[:2])
    return parts[0]


def main():
    root = sys.argv[1] if len(sys.argv) > 1 and not sys.argv[1].startswith("--") else "."
    totals = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for f in filenames:
            lang = EXTS.get(os.path.splitext(f)[1])
            if not lang:
                continue
            rel = os.path.relpath(os.path.join(dirpath, f), root)
            key = (component(rel), lang, "test" if is_test(rel) else "prod")
            totals[key] = totals.get(key, 0) + sloc(os.path.join(dirpath, f))
    out = [{"component": c, "lang": l, "kind": k, "lines": n} for (c, l, k), n in sorted(totals.items())]
    if "--json" in sys.argv:
        json.dump(out, open(sys.argv[sys.argv.index("--json") + 1], "w"), indent=1)
    by_lang = {}
    for row in out:
        by_lang[(row["lang"], row["kind"])] = by_lang.get((row["lang"], row["kind"]), 0) + row["lines"]
    for (lang, kind), n in sorted(by_lang.items()):
        print(f"{lang:10s} {kind:5s} {n:9,d}")


if __name__ == "__main__":
    main()
