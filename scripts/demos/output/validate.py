#!/usr/bin/env python3
"""Validate parser outcomes, original records, arrival order, and output routing."""
import json
import pathlib
import sys


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def main():
    check(len(sys.argv) == 3, "usage: validate.py CAPTURE_DIRECTORY FIXTURES_JSON")
    directory = pathlib.Path(sys.argv[1])
    fixture = json.loads(pathlib.Path(sys.argv[2]).read_text())
    before = directory / "data" / "before"
    after = directory / "data" / "after"
    controls = directory / "data" / "controls"
    before_json = (before / "model.json").read_bytes()
    check(b"\x1b" in before_json, "baseline did not reproduce forced-color pipe decoration")
    try:
        json.loads(before_json)
    except json.JSONDecodeError:
        pass
    else:
        raise SystemExit("baseline forced-color output unexpectedly parsed as JSON")
    after_json = (after / "model.json").read_bytes()
    check(b"\x1b" not in after_json, "candidate still decorates piped JSON")
    check(json.loads(after_json) == fixture["model"], "candidate JSON data changed")

    for scene, path in [("before", before), ("after", after)]:
        data = (path / "events.jsonl").read_bytes()
        lines = data.splitlines()
        check(len(lines) == 2 and [json.loads(line) for line in lines] == fixture["events"],
              f"{scene}: JSONL must preserve both original records")
        check(b"\x1b" not in data, f"{scene}: JSONL consumer received decoration")
        arrivals = json.loads((path / "arrivals.json").read_text())
        check([item["type"] for item in arrivals] == [item["type"] for item in fixture["events"]],
              f"{scene}: arrival records changed order")
        gap = arrivals[1]["seconds"] - arrivals[0]["seconds"]
        if scene == "before":
            check(gap < 0.5, "baseline no longer groups the delayed events")
        else:
            check(gap >= 1.5, "candidate did not expose the first event before the delayed second event")
    check((before / "events.jsonl").read_bytes() == (after / "events.jsonl").read_bytes(),
          "event bytes differ between binaries")

    expected_data = b"ID: demo-model\nOwned by: demo-project\n"
    check((controls / "quiet.txt").read_bytes() == expected_data, "quiet discarded or changed selected data")
    check((controls / "quiet.stderr").read_bytes() == b"", "quiet printed optional feedback")
    check((controls / "model.txt").read_bytes() == expected_data, "verbose contaminated stdout")
    expected_details = ("Summary; use --format json for full data.\n"
                        "Command: models retrieve\nFormat option: auto\nCommand result: completed\n")
    check((controls / "details.txt").read_text() == expected_details, "verbose stderr contract changed")

    model_request = {"method": "GET", "path": "/v1/models/demo-model"}
    stream_request = {"method": "POST", "path": "/v1/responses",
                      "body": {"model": "demo-model", "input": "Say hi", "stream": True}}
    requests = [json.loads(line) for line in (directory / "requests.jsonl").read_text().splitlines()]
    check(requests == [model_request, stream_request, model_request, stream_request, model_request, model_request],
          "synthetic request count, sequence, or content changed")
    statuses = []
    for scene in ["before", "after"]:
        statuses.extend([f"{scene}\tjson-cli\t0", f"{scene}\tjson-tee\t0",
                         f"{scene}\tjson-parser\t{1 if scene == 'before' else 0}",
                         f"{scene}\tstream-cli\t0", f"{scene}\tstream-consumer\t0"])
    statuses.extend(["controls\tquiet\t0", "controls\tverbose\t0"])
    check((directory / "statuses.tsv").read_text().splitlines() == statuses, "unexpected process statuses")
    for scene in ["before", "after", "controls"]:
        transcript = (directory / f"{scene}.txt").read_text()
        check("$ openai" in transcript or "$ FORCE_COLOR=1 openai" in transcript, f"{scene}: missing real command")
        check("[exit " not in transcript, f"{scene}: visible exit footer")
        check("synthetic-demo-key" not in transcript, f"{scene}: fixture key entered the recording")
    print("PASS: six synthetic requests, twelve process statuses, JSON/JSONL bytes, event timing, and stderr routing")


if __name__ == "__main__":
    main()
