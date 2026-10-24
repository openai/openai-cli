#!/usr/bin/env python3
"""Check real CLI transcripts and synthetic fixture request identity."""

import json
import pathlib
import re
import sys


output = pathlib.Path(sys.argv[1])
metadata = (output / "metadata.txt").read_text()
scenes = ("before-body", "after-body", "before-stream", "after-stream", "after-limits")
transcripts = {scene: (output / f"{scene}.txt").read_text() for scene in scenes}
for scene, transcript in transcripts.items():
    kind = scene.split("-")[0]
    source = re.search(rf"(?m)^{kind} source commit: ([0-9a-f]{{40}})$", metadata).group(1)
    assert f"Source commit: {source}" in transcript, scene
    version = re.search(r"(?m)^openai version (\S+)\s*$", transcript).group(1)
    assert "$ openai --version" in transcript, scene
    assert f"User-Agent: OpenAI/CLI {version}" in transcript, scene
    assert "Terminal replay | synthetic loopback API | fake credentials" in transcript, scene
    assert "Authorization: Bearer <REDACTED>" in transcript, scene
    if scene.startswith("after-"):
        assert re.search(r"HTTP attempt 1: response headers received after \d+ ms", transcript), scene
    else:
        assert "HTTP attempt" not in transcript, (scene, "baseline already reported HTTP timing")
    # The echoed command intentionally displays its synthetic prompt.
    diagnostics = transcript.replace("--input 'synthetic private prompt'", "--input '<fixture>'")
    for private in ("synthetic-demo-key", "synthetic private prompt", "synthetic private response",
                    "synthetic private heartbeat", "synthetic answer"):
        assert private not in diagnostics, (scene, private)
    assert "total request time" not in transcript.lower(), scene

for scene in ("before-body", "before-stream"):
    for label in (" body (redacted;", "SSE frame ", "SSE event "):
        assert label not in transcripts[scene], (scene, label)

for scene in ("before-body", "after-body"):
    usage = [json.JSONDecoder().raw_decode(transcripts[scene][match.start():])[0]
             for match in re.finditer(r'(?m)^\{', transcripts[scene])]
    assert {"input_tokens": 3, "output_tokens": 2, "total_tokens": 5} in usage, scene

body = transcripts["after-body"]
assert "request body (redacted;" in body
assert "response body (redacted;" in body
request_preview = re.search(r'request body \(redacted;[^\r\n]*', body).group()
response_preview = re.search(r'response body \(redacted;[^\r\n]*', body).group()
for value in ('"input":"<REDACTED>"', '"store":false', '"max_output_tokens":8'):
    assert value in request_preview, value
assert '"total_tokens":5' in response_preview

event_types = ["response.created", "response.output_text.delta", "response.completed"]
timings = {}
for scene in ("before-stream", "after-stream", "after-limits"):
    actual_types = [line.strip() for line in transcripts[scene].splitlines() if line.strip() in event_types]
    assert actual_types == event_types, (scene, actual_types)
for scene, expected in (("after-stream", 3), ("after-limits", 1)):
    events = re.findall(r"SSE event (\d+) complete frame observed after (\d+) ms", transcripts[scene])
    assert [int(number) for number, _ in events] == list(range(1, expected + 1)), (scene, events)
    elapsed = [int(duration) for _, duration in events]
    assert elapsed == sorted(elapsed), (scene, elapsed)
    timings[scene] = elapsed
assert "SSE frame " in transcripts["after-stream"]
assert r"\r\n" in transcripts["after-stream"]
assert "SSE capture limit reached" in transcripts["after-limits"]
assert "SSE diagnostics truncated by capture limits" in transcripts["after-limits"]

requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert len(requests) == 5, requests
assert all(row["path"] == "/responses" and row["status"] == 200 for row in requests)
assert [row["stream"] for row in requests] == [False, False, True, True, True]
assert requests[0]["request_sha256"] == requests[1]["request_sha256"]
assert len({row["request_sha256"] for row in requests[2:]}) == 1
print(json.dumps({"result": "PASS", "requests": len(requests),
                  "actual_cli_event_elapsed_ms": timings}, indent=2))
