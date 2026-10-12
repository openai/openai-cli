#!/usr/bin/env python3
"""Check recorded command statuses, extracted output, and synthetic request headers."""

import json
import pathlib
import sys


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def main():
    check(len(sys.argv) == 2, "usage: validate.py CAPTURE_DIRECTORY")
    directory = pathlib.Path(sys.argv[1])
    commands = [
        "$ openai --project proj-example models list --transform=id -r",
        "$ openai models --project proj-example list --transform=id -r",
        "$ openai models list --project proj-example --transform=id -r",
    ]
    labels = ["1. Before the command", "2. Between command words", "3. After the command"]
    summaries = {
        "before": "--project works only before the command.",
        "after": "--project works in all three positions.",
    }
    error = "An option is not recognized. Check the command's available options with --help."
    for scene, count in [("before", 1), ("after", 3)]:
        transcript = (directory / f"{scene}.txt").read_text()
        expected_lines = [scene.upper(), summaries[scene], ""]
        for index, (label, command) in enumerate(zip(labels, commands)):
            output = "OpenAI-Project: proj-example" if index < count else error
            expected_lines.extend([label, command, output, ""])
        expected_lines.append("Synthetic API: output shows the received project header.")
        check(transcript.splitlines() == expected_lines, f"{scene}: unexpected command/output sequence")

    requests = [json.loads(line) for line in (directory / "requests.jsonl").read_text().splitlines()]
    expected = {"method": "GET", "path": "/v1/models", "project": "proj-example"}
    check(requests == [expected] * 4, "expected four requests with the explicit project header")
    statuses = (directory / "statuses.tsv").read_text().splitlines()
    check(statuses == [
        "before\troot\t0", "before\tgroup\t1", "before\tleaf\t1",
        "after\troot\t0", "after\tgroup\t0", "after\tleaf\t0",
    ], "unexpected command exit statuses")
    print("PASS: six command statuses, four extracted results, and four project headers")


if __name__ == "__main__":
    main()
