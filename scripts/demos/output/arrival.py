#!/usr/bin/env python3
"""Show consumer arrival times while retaining the exact CLI event bytes."""
import json
import pathlib
import sys
import time

started = time.monotonic()
arrivals = []
with pathlib.Path("events.jsonl").open("xb") as output:
    for line in sys.stdin.buffer:
        elapsed = time.monotonic() - started
        event = json.loads(line)
        output.write(line)
        arrivals.append({"seconds": elapsed, "type": event["type"]})
        print(f'{elapsed:4.2f}s  {event["type"]}', flush=True)
with pathlib.Path("arrivals.json").open("x") as output:
    json.dump(arrivals, output, indent=2)
    output.write("\n")
if len(arrivals) != 2:
    raise SystemExit("Expected exactly two synthetic events")
