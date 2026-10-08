#!/usr/bin/env python3
"""Validate local utility transcripts and recorded process exit statuses."""

import json
import pathlib
import re
import sys


COMMANDS = {
    "count": '$ openai tokenizer count --text "Hello, world!"',
    "inspect": '$ openai tokenizer inspect --text "Hi!"',
    "codex": "$ openai codex --destination config",
    "guide": "$ openai codex",
    "editor": "$ openai tokenizer",
}
CONFIG_URL = "https://learn.chatgpt.com/docs/config-file/config-basic"


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def validate(directory, mode):
    check(mode in COMMANDS, "MODE must be count, inspect, codex, guide, or editor")
    before_status = 0 if mode == "editor" else 3 if mode == "guide" else 1
    after_status = 130 if mode == "editor" else 0
    expected = [f"before\t{before_status}", f"after\t{after_status}"]
    if (directory / "expected-statuses.tsv").exists():
        expected = (directory / "expected-statuses.tsv").read_text().splitlines()
        check(len(expected) == 2 and expected[0].startswith("before\t") and
              expected[1] == f"after\t{after_status}", "invalid expected status record")
        before_status = int(expected[0].split("\t")[1])
        check(before_status in (0, 1, 3), "unsupported baseline status")
    statuses = (directory / "statuses.tsv").read_text().splitlines()
    check(statuses == expected, "unexpected command exit statuses")
    transcripts = {}
    for scene in ["before", "after"]:
        text = (directory / f"{scene}.txt").read_text()
        check(scene.upper() in text.splitlines(), f"{scene}: missing scene label")
        check(COMMANDS[mode] in text, f"{scene}: missing actual command")
        check(text.rstrip().endswith("$"), f"{scene}: missing completed scene prompt")
        for unwanted in ["synthetic-demo-key", "Authorization:", "Bearer ", "[exit ", "--open"]:
            check(unwanted not in text, f"{scene}: unexpected credential, status footer, or browser flag")
        transcripts[scene] = text

    before = transcripts["before"]
    after = transcripts["after"]
    error = "Unknown help topic." if mode == "guide" else "An option is not recognized."
    if before_status != 0:
        check(error in before, "before: expected the baseline command failure")
    elif mode == "editor":
        check("USAGE:" in before and "Count and inspect" in before,
              "before: expected the published tokenizer help")
    elif mode == "guide":
        check("npm install -g @openai/codex" in before, "before: expected the published plain Codex guide")
    check(error not in after, "after: retained the baseline command failure")
    if mode in ["count", "inspect"]:
        byte_count, token_count = (13, 4) if mode == "count" else (3, 2)
        for line in ["Encoding: o200k_base", f"Input bytes: {byte_count}", f"Tokens: {token_count}"]:
            check(line in after.splitlines(), f"after: missing exact result {line!r}")
        if mode == "inspect":
            check(re.search(r"\b4869\b", after) is not None, "after: missing exact Hi token bytes")
            check(re.search(r"\b21\b", after) is not None, "after: missing exact exclamation token bytes")
            check('"Hi"' in after and '"!"' in after, "after: missing readable token fragments")
    elif mode == "codex":
        check(CONFIG_URL in after.splitlines(), "after: missing exact configuration URL")
        check("npm install" not in after, "after: destination mode unexpectedly printed the setup guide")
    elif mode == "guide":
        check("Codex CLI is a separate command named codex." in after or
              "A separate CLI named codex." in after, "after: Codex CLI identity is missing")
        for value in [
            "npm install -g @openai/codex",
            "brew install --cask codex",
            "~/.codex/config.toml",
            ".codex/config.toml",
            'approval_policy = "on-request"',
            'sandbox_mode = "workspace-write"',
            CONFIG_URL,
        ]:
            check(value in after, f"after: missing instruction {value!r}")
    else:
        validate_editor(directory)
    print(f"PASS: {mode} command transcripts and before/after exit statuses")


def validate_editor(directory):
    report = json.loads((directory / "editor-input.json").read_text())
    check(report["input"] == "Hello, tokens! 👋\nCafé." and report["input_bytes"] == 26,
          "editor: synthetic input changed")
    check(report["exit_status"] == 130, "editor: Ctrl+C status changed")
    stages = [
        ("text", re.compile(r"\d+ tokens · 26 bytes")),
        ("ids", re.compile(r"\[Token IDs\]")),
        ("bytes", re.compile(r"\[Bytes\]")),
        ("details", re.compile(r"Token details · exact bytes")),
        ("encoding", re.compile(r"Encoding  cl100k_base")),
        ("controls", re.compile(r"Tokenizer · controls")),
    ]
    check([event["state"] for event in report["states"]] == [name for name, _ in stages],
          "editor: incomplete input-driver states")
    events = [json.loads(line) for line in (directory / "after.cast").read_text().splitlines()]
    check(events[0]["width"] == report["columns"] and events[0]["height"] == report["rows"],
          "editor: inner and recorded terminal dimensions differ")
    check("partial UTF-8" in "".join(event[2] for event in events[1:] if event[1] == "o"),
          "editor: exact bytes for the partial Unicode token were not shown")
    output = ""
    snapshots = []
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
    for event in events[1:]:
        if event[1] != "o":
            continue
        output = (output + event[2])[-65536:]
        plain = ansi.sub("", output)
        if len(snapshots) < len(stages):
            name, pattern = stages[len(snapshots)]
            if pattern.search(plain):
                timestamp = float(event[0]) + 0.25
                if snapshots:
                    check(timestamp - snapshots[-1][1] > 0.5,
                          "editor: stages did not remain visible as separate frames")
                snapshots.append((name, timestamp))
                output = ""
    check(len(snapshots) == len(stages), "editor: recording omitted a required visible state")
    check(snapshots[-1][1] < float(events[-1][0]), "editor: snapshot occurs after recording ends")
    (directory / "editor-snapshots.tsv").write_text(
        "".join(f"{name}\t{timestamp:.6f}\n" for name, timestamp in snapshots))
    print("PASS: real editor text, IDs, bytes, details, encoding, controls, and Ctrl+C")


def main():
    check(len(sys.argv) == 3, "usage: validate.py CAPTURE_DIRECTORY MODE")
    validate(pathlib.Path(sys.argv[1]), sys.argv[2])


if __name__ == "__main__":
    main()
