#!/usr/bin/env python3
"""Validate local utility transcripts and recorded process exit statuses."""

import pathlib
import re
import sys


COMMANDS = {
    "count": '$ openai tokenizer count --text "Hello, world!"',
    "inspect": '$ openai tokenizer inspect --text "Hi!"',
    "codex": "$ openai codex --destination config",
    "guide": "$ openai codex",
}
CONFIG_URL = "https://learn.chatgpt.com/docs/config-file/config-basic"


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def validate(directory, mode):
    check(mode in COMMANDS, "MODE must be count, inspect, codex, or guide")
    before_status = 3 if mode == "guide" else 1
    statuses = (directory / "statuses.tsv").read_text().splitlines()
    check(statuses == [f"before\t{before_status}", "after\t0"], "unexpected command exit statuses")
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
    check(error in before, "before: expected the baseline command failure")
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
    else:
        for value in [
            "Codex CLI is a separate command named codex.",
            "npm install -g @openai/codex",
            "brew install --cask codex",
            "~/.codex/config.toml",
            ".codex/config.toml",
            'approval_policy = "on-request"',
            'sandbox_mode = "workspace-write"',
            CONFIG_URL,
        ]:
            check(value in after, f"after: missing instruction {value!r}")
    print(f"PASS: {mode} command transcripts and before/after exit statuses")


def main():
    check(len(sys.argv) == 3, "usage: validate.py CAPTURE_DIRECTORY MODE")
    validate(pathlib.Path(sys.argv[1]), sys.argv[2])


if __name__ == "__main__":
    main()
