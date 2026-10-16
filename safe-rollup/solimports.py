#!/usr/bin/env python3
"""Lists contracts-bedrock files that transitively import a missing file.

Usage: solimports.py <contracts-bedrock dir>
Prints one path per line, relative to the contracts-bedrock dir.
"""
import os
import re
import sys

root = os.path.abspath(sys.argv[1])
remappings = {
    "@openzeppelin/contracts-upgradeable/": "lib/openzeppelin-contracts-upgradeable/contracts/",
    "@openzeppelin/contracts/": "lib/openzeppelin-contracts/contracts/",
    "@openzeppelin/contracts-v5/": "lib/openzeppelin-contracts-v5/contracts/",
    "@rari-capital/solmate/": "lib/solmate/",
    "@lib-keccak/": "lib/lib-keccak/contracts/lib/",
    "@solady/": "lib/solady/src/",
    "forge-std/": "lib/forge-std/src/",
    "ds-test/": "lib/forge-std/lib/ds-test/src/",
    "safe-contracts/": "lib/safe-contracts/contracts/",
    "kontrol-cheatcodes/": "lib/kontrol-cheatcodes/src/",
    "solady/": "lib/solady/",
}
IMPORT = re.compile(r'^\s*import\s+(?:[^"\']*from\s+)?["\']([^"\']+)["\']', re.M)


def resolve(importer, path):
    for prefix, target in remappings.items():
        if path.startswith(prefix):
            return os.path.normpath(target + path[len(prefix):])
    if path.startswith("."):
        return os.path.normpath(os.path.join(os.path.dirname(importer), path))
    return os.path.normpath(path)


files = []
for top in ("src", "interfaces", "scripts", "test"):
    for dirpath, _, names in os.walk(os.path.join(root, top)):
        files += [os.path.relpath(os.path.join(dirpath, n), root) for n in names if n.endswith(".sol")]

imports = {}
for f in files:
    text = open(os.path.join(root, f), encoding="utf-8").read()
    imports[f] = [resolve(f, p) for p in IMPORT.findall(text)]

broken = set()
changed = True
while changed:
    changed = False
    for f, deps in imports.items():
        if f in broken:
            continue
        if any(d in broken or not os.path.exists(os.path.join(root, d)) for d in deps):
            broken.add(f)
            changed = True

for f in sorted(broken):
    print(f)
