#!/usr/bin/env python3
"""Check the captured CLI output and the synthetic fixture's measured delays."""

import json
import pathlib
import re
import sys


output = pathlib.Path(sys.argv[1])
before = (output / "before.txt").read_text()
after = (output / "after.txt").read_text()
assert "HTTP attempt" not in before, "baseline already reported HTTP attempt timing"
for transcript in (before, after):
    assert "$ openai --debug models retrieve model_synthetic" in transcript
    assert re.search(r"(?m)^ID: model_synthetic\s*$", transcript), "missing displayed model ID"
    assert re.search(r"(?m)^Owned by: synthetic\s*$", transcript), "missing displayed model owner"
    assert "synthetic-demo-key" not in transcript
    assert "Authorization: Bearer <REDACTED>" in transcript
    assert "total request" not in transcript.lower()

stages = ["response headers received", "first response data read", "response body fully consumed"]
elapsed = []
for stage in stages:
    matches = re.findall(r"HTTP attempt 1: " + stage + r" after (\d+) ms", after)
    assert len(matches) == 1, (stage, matches)
    elapsed.append(int(matches[0]))
assert elapsed[0] >= 150, elapsed
assert elapsed[1] - elapsed[0] >= 180, elapsed
assert elapsed[2] - elapsed[1] >= 180, elapsed
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert len(requests) == 2, requests
assert all(request["path"] == "/models/model_synthetic" and request["status"] == 200 for request in requests)
print(json.dumps({"result": "PASS", "requests": len(requests), "actual_cli_elapsed_ms": dict(zip(stages, elapsed))}, indent=2))
