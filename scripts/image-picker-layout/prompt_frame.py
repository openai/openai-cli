#!/usr/bin/env python3
"""Find a complete initial prompt frame in an actual asciinema recording."""
import json
from pathlib import Path
import sys

events = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()][1:]
first = next(i for i, event in enumerate(events)
             if event[1] == "o" and "A tiny orange robot" in event[2])
last = first
# The painter can split one frame across output events. Include that burst.
# The driver holds this state for two seconds before the first navigation key.
for index in range(first + 1, len(events)):
    if events[index][0] - events[first][0] > 0.2:
        break
    last = index
print(f"event:{last}")
