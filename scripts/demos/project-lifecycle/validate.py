#!/usr/bin/env python3
"""Verify actual command output and all six synthetic requests."""

import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
before = (root / "before.txt").read_text()
after = (root / "after.txt").read_text()
for action in ("created", "updated", "archived"):
    receipt = f"Project proj_demo {action}."
    assert receipt in after, receipt
    assert receipt not in before, receipt
for text in ("Name: Demo", "Name: Renamed", "Status: active", "Status: archived", "Residency: GLOBAL"):
    assert text in before and text in after, text
assert "Archive is not a delete operation." in after
records = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
assert len(records) == 6, records
assert records[:3] == records[3:], records
assert [item["path"] for item in records[:3]] == [
    "/v1/organization/projects",
    "/v1/organization/projects/proj_demo",
    "/v1/organization/projects/proj_demo/archive",
]
print("PASS: six matching requests; returned lifecycle receipts; archive semantics.")
