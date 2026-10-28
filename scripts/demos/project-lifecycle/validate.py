#!/usr/bin/env python3
"""Verify actual command output and all six synthetic requests."""

import json
import pathlib
import sys


def require(condition, detail):
    if not condition:
        raise SystemExit(f"FAIL: {detail}")


root = pathlib.Path(sys.argv[1])
before = (root / "before.txt").read_text()
after = (root / "after.txt").read_text()
for action in ("created", "updated", "archived"):
    receipt = f"Project proj_demo {action}."
    require(receipt in after, f"missing receipt: {receipt}")
    require(receipt not in before, f"receipt in baseline: {receipt}")
for text in ("Name: Demo", "Name: Renamed", "Status: active", "Status: archived", "Residency: GLOBAL"):
    require(text in before and text in after, f"missing field: {text}")
require("Archive is not a delete operation." in after, "missing archive guidance")
records = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
require(len(records) == 6, "expected six requests")
require(records[:3] == records[3:], "request bodies differ")
require([item["path"] for item in records[:3]] == [
    "/v1/organization/projects",
    "/v1/organization/projects/proj_demo",
    "/v1/organization/projects/proj_demo/archive",
], "unexpected request paths")
print("PASS: six matching requests; returned lifecycle receipts; archive semantics.")
