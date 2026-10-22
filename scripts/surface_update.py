#!/usr/bin/env python3
"""Sync hardcoded tool counters with testdata/golden.json.

Part of `make surface-update`: after golden.json and the README table
are regenerated, the registered-tools count must match everywhere it is
asserted by hand — internal/tools/golden_test.go (expectedToolCount)
and internal/server/server_test.go (tools/list check).
"""

import json
import pathlib
import re
import sys

root = pathlib.Path(__file__).resolve().parent.parent
golden = root / "internal/tools/testdata/golden.json"

try:
    count = len(json.loads(golden.read_text()))
except Exception as e:
    sys.exit(f"cannot read {golden}: {e}")

targets = [
    (
        root / "internal/tools/golden_test.go",
        [(r"const expectedToolCount = \d+", f"const expectedToolCount = {count}")],
    ),
    (
        root / "internal/server/server_test.go",
        [
            (r"len\(res\.Tools\) != \d+", f"len(res.Tools) != {count}"),
            (r"tools/list = %d, want \d+", f"tools/list = %d, want {count}"),
        ],
    ),
]

for path, subs in targets:
    text = path.read_text()
    for pat, repl in subs:
        text, n = re.subn(pat, repl, text)
        if n == 0:
            sys.exit(f"{path}: pattern {pat!r} not found — did the assertion move?")
    path.write_text(text)

print(f"surface counters synced: {count} tools")
