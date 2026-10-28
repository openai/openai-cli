"""Validate synthetic recorded events, matching requests, and live text timing."""

import json
import pathlib
import sys


def main():
    output = pathlib.Path(sys.argv[1])
    fixture = json.loads(pathlib.Path(sys.argv[2]).read_text())
    before = (output / "before.txt").read_text()
    after = (output / "after.txt").read_text()
    explicit = (output / "explicit-jsonl.txt").read_text()
    assert before.count("Type: transcript.text.delta") == 2, before
    assert before.count("Hello, spoken world.") == 1, before
    assert after.count("Hello, spoken world.") == 1, after
    assert "Type: transcript.text.delta" not in after, after
    assert "Total tokens: 9" in before and "Total tokens: 9" in after
    decoder = json.JSONDecoder()
    objects = []
    remainder = explicit[explicit.index("{"):]
    while remainder.startswith("{"):
        value, end = decoder.raw_decode(remainder)
        objects.append(value)
        remainder = remainder[end:].lstrip()
    assert objects == fixture, objects
    requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
    expected = {"method": "POST", "path": "/v1/audio/transcriptions", "body": {
        "model": "demo-model", "file": "sample.wav", "stream": True}}
    assert requests == [expected] * 3, requests
    capture = [json.loads(line) for line in (output / "after.cast").read_text().splitlines()]
    visible = ""
    observed = {}
    for event in capture[1:]:
        if event[1] != "o":
            continue
        visible += event[2]
        for marker in ("Hello, ", "spoken world.", "Total tokens: 9"):
            if marker not in observed and marker in visible:
                observed[marker] = event[0]
    assert observed["spoken world."] - observed["Hello, "] >= 0.3, observed
    assert observed["Total tokens: 9"] - observed["spoken world."] >= 0.3, observed
    print("PASS: matching synthetic requests, complete explicit JSONL, deduplicated text and usage")
    print("After-scene observed output times (seconds): " + json.dumps(observed, sort_keys=True))


if __name__ == "__main__":
    main()
