#!/usr/bin/env python3
"""Check recorded command statuses, JSON output, and synthetic request headers."""

import json
import pathlib
import sys


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def json_objects(text):
    decoder = json.JSONDecoder()
    objects = []
    while "{" in text:
        start = text.index("{")
        value, end = decoder.raw_decode(text[start:])
        objects.append(value)
        text = text[start + end:]
    return objects


def main():
    check(len(sys.argv) == 2, "usage: validate.py CAPTURE_DIRECTORY")
    directory = pathlib.Path(sys.argv[1])
    commands = [
        "$ openai --project proj-example --format=json models list",
        "$ openai models --project proj-example --format=json list",
        "$ openai models list --project proj-example --format=json",
    ]
    model = {
        "created": 0,
        "id": "OpenAI-Project: proj-example",
        "object": "model",
        "owned_by": "synthetic",
    }
    error = "An option is not recognized. Check the command's available options with --help."
    for scene, count in [("before", 1), ("after", 3)]:
        transcript = (directory / f"{scene}.txt").read_text()
        for command in commands:
            check(transcript.count(command) == 1, f"{scene}: missing or repeated command: {command}")
        check(json_objects(transcript) == [model] * count, f"{scene}: unexpected JSON output")
        check(transcript.count(error) == 3 - count, f"{scene}: unexpected diagnostic count")

    requests = [json.loads(line) for line in (directory / "requests.jsonl").read_text().splitlines()]
    expected = {"method": "GET", "path": "/v1/models", "project": "proj-example"}
    check(requests == [expected] * 4, "expected four requests with the explicit project header")
    statuses = (directory / "statuses.tsv").read_text().splitlines()
    check(statuses == [
        "before\troot\t0", "before\tgroup\t1", "before\tleaf\t1",
        "after\troot\t0", "after\tgroup\t0", "after\tleaf\t0",
    ], "unexpected command exit statuses")
    print("PASS: six command statuses, four JSON results, and four project headers")


if __name__ == "__main__":
    main()
