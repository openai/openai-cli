#!/usr/bin/env python3
"""Validate local utility transcripts and recorded process exit statuses."""

import json
import pathlib
import re
import sys


MODEL_NAMES = {"o200k_base": "GPT-5.x & o1/o3", "cl100k_base": "GPT-4 & GPT-3.5",
               "r50k_base": "GPT-3", "p50k_base": "Codex"}


def model_choices_complete(plain):
    rows = [line.strip().removeprefix("› ").strip() for line in plain.splitlines()]
    checked = 0
    for encoding, label in MODEL_NAMES.items():
        badge = "Default" if encoding == "o200k_base" else "Legacy"
        matches = [row for row in rows if re.fullmatch(re.escape(label) + r"(?: ✓)?[ \t]+" + badge, row)]
        if len(matches) != 1:
            return False
        checked += " ✓" in matches[0]
    return checked == 1


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
    layouts = editor_layouts(directory) if mode == "editor" else {}
    before_status = 3 if mode == "guide" else 1
    if mode == "editor":
        before_status = 0 if layouts["before"] == "help" else 130
    after_status = 130 if mode == "editor" else 0
    expected = [f"before\t{before_status}", f"after\t{after_status}"]
    if (directory / "expected-statuses.tsv").exists():
        expected = (directory / "expected-statuses.tsv").read_text().splitlines()
        check(len(expected) == 2 and expected[0].startswith("before\t") and
              expected[1] == f"after\t{after_status}", "invalid expected status record")
        before_status = int(expected[0].split("\t")[1])
        allowed = (0, 1, 3)
        if mode == "editor":
            allowed = (0,) if layouts["before"] == "help" else (130,)
        check(before_status in allowed, "unsupported baseline status")
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
    if mode == "editor" and layouts["before"] != "help":
        validate_editor(directory, "before", layouts["before"])
    elif before_status != 0:
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
        validate_editor(directory, "after", layouts["after"])
    print(f"PASS: {mode} command transcripts and before/after exit statuses")


def editor_layouts(directory):
    path = directory / "editor-layouts.tsv"
    if not path.exists():
        report = json.loads((directory / "editor-input.json").read_text())
        return {"before": "help", "after": report.get("layout", "legacy")}
    rows = [row.split("\t") for row in path.read_text().splitlines()]
    check(len(rows) == 2 and all(len(row) == 2 for row in rows) and
          rows[0][0] == "before" and rows[1][0] == "after", "invalid editor layout record")
    layouts = dict(rows)
    check(layouts["before"] in ("help", "legacy", "options") and layouts["after"] == "options",
          "unsupported editor layout comparison")
    return layouts


def validate_editor(directory, scene="after", layout="legacy"):
    report_name = "editor-input.json" if scene == "after" else "before-editor-input.json"
    report = json.loads((directory / report_name).read_text())
    check(layout in ("legacy", "options") and report.get("layout", "legacy") == layout,
          f"{scene}: input driver used the wrong editor layout")
    check(report["input"] == "Hello, tokens! 👋\nCafé." and report["input_bytes"] == 26,
          "editor: synthetic input changed")
    check(report["exit_status"] == 130, "editor: Ctrl+C status changed")
    capture_version = report.get("capture_version", 1)
    check(capture_version in (1, 2, 3, 4), "editor: unsupported capture version")
    linked = capture_version >= 3
    models = capture_version == 4
    linked_path = directory / "editor-linked.tsv"
    linked_expected = False
    if linked_path.exists():
        settings = linked_path.read_text().splitlines()
        check(len(settings) == 2 and settings[0] == "before\t0" and
              settings[1] in ("after\t0", "after\t1"), "editor: invalid linked recording setting")
        linked_expected = scene == "after" and settings[1] == "after\t1"
    check(linked == linked_expected, f"{scene}: linked recording version differs from requested mode")
    presentation = "encodings"
    presentation_path = directory / "editor-presentation.tsv"
    if presentation_path.exists():
        settings = presentation_path.read_text().splitlines()
        check(len(settings) == 2 and settings[0] == "before\tencodings" and
              settings[1] in ("after\tencodings", "after\tmodels"), "editor: invalid presentation setting")
        if scene == "after":
            presentation = settings[1].split("\t")[1]
    check(models == (presentation == "models") and report.get("presentation", "encodings") == presentation,
          f"{scene}: presentation version differs from requested mode")

    def encoding_row(encoding):
        if models:
            return "Model  " + MODEL_NAMES[encoding] + "  " + ("Default" if encoding == "o200k_base" else "Legacy")
        return ("Tokenizer" if layout == "options" else "Encoding") + "  " + encoding

    if linked:
        check(scene == "after" and layout == "options", "editor: linked mode requires after Options layout")
        check(report.get("caret_actions") == {"keys": ["Home", "Right"], "expected_byte_offset": 21},
              "editor: linked caret actions changed")
        check(report.get("tokenizer_actions") == ["cl100k_base", "r50k_base", "p50k_base"],
              "editor: legacy tokenizer actions changed")
    if "layout" in report:
        check(report.get("input_actions") == {"typed": "Hello, ", "pasted": ["tokens! 👋", "Café."], "newline": "Enter"},
              "editor: exact paste or Text Enter input changed")
    stages = [
        ("text", re.compile(r"10 tokens[ \t]*(?:[\r\n]|$)" if layout == "options" else r"10 tokens · 26 bytes")),
        ("ids", re.compile(r"\[Token IDs\]")),
        ("bytes", re.compile(r"\[Bytes\]")),
        ("details", re.compile(r"Token details · exact bytes")),
        ("encoding", re.compile(re.escape(encoding_row("cl100k_base")))),
        ("controls", re.compile(r"Tokenizer · controls")),
    ]
    if layout == "options":
        stages.insert(2, ("view-choice", re.compile(r"Choose view")))
        stages.insert(5, ("tokenizer-choice", re.compile("Choose model" if models else "Choose tokenizer")))
    if capture_version >= 2:
        details_index = next(index for index, (name, _) in enumerate(stages) if name == "details")
        position = r"Token 5 of 10\b" if layout == "options" else r"Token 5/10 · ID 61138\b"
        stages.insert(details_index, ("results", re.compile(position)))
    if linked:
        stages.insert(1, ("caret", re.compile(r"Token 9 of 10\b")))
        controls_index = next(index for index, (name, _) in enumerate(stages) if name == "controls")
        stages[controls_index:controls_index] = [
            ("r50k", re.compile(re.escape(encoding_row("r50k_base")))),
            ("p50k", re.compile(re.escape(encoding_row("p50k_base")))),
        ]
    check([event["state"] for event in report["states"]] == [name for name, _ in stages],
          "editor: incomplete input-driver states")
    events = [json.loads(line) for line in (directory / f"{scene}.cast").read_text().splitlines()]
    check(events[0]["width"] == report["columns"] and events[0]["height"] == report["rows"],
          "editor: inner and recorded terminal dimensions differ")
    output = ""
    snapshots = []
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
    for event in events[1:]:
        if event[1] != "o":
            continue
        output = (output + event[2])[-65536:]
        if linked:
            # Linked versions need one coherent frame: all three alternate
            # tokenizers have the same fixture count, including stale results.
            output = output.rsplit("\r\x1b[J", 1)[-1]
        plain = ansi.sub("", output)
        if len(snapshots) < len(stages):
            name, pattern = stages[len(snapshots)]
            complete = pattern.search(plain)
            if linked:
                complete = complete and "Ctrl+C exit" in plain
            if name == "results":
                complete = complete and "›[20 f0 9f 91]" in plain and "Enter details" in plain
                complete = complete and "Token details · exact bytes" not in plain
            elif name == "details":
                complete = complete and all(value in plain for value in ["partial UTF-8", "ID 61138", "20 f0 9f 91"])
                complete = complete and re.search(r"bytes\s+\[14,\s*18\)", plain)
                if models:
                    complete = complete and "Encoding o200k_base" in plain
            elif name == "encoding":
                complete = complete and "11 tokens · 26 bytes" in plain
            elif name == "caret":
                complete = complete and all(value in plain for value in ['·["afé"]', "C▏afé.", encoding_row("o200k_base")])
                complete = complete and re.search(r"10 tokens[ \t]*(?:[\r\n]|$)", plain)
            elif name in ("r50k", "p50k"):
                complete = complete and "11 tokens · 26 bytes" in plain and "Updating" not in plain
            elif layout == "options":
                if name == "text":
                    complete = complete and "View  " in plain and encoding_row("o200k_base") in plain
                elif name == "ids":
                    complete = complete and "View  " in plain
                elif name == "bytes":
                    complete = complete and "10 tokens · 26 bytes" in plain and "View  " in plain
                elif name == "view-choice":
                    complete = complete and all(value in plain for value in ["Text", "Token IDs", "Bytes", "Esc cancel"])
                elif name == "tokenizer-choice":
                    if models:
                        complete = complete and model_choices_complete(plain) and "Esc cancel" in plain
                    else:
                        complete = complete and all(value in plain for value in ["o200k_base", "cl100k_base", "Esc cancel"])
                    if linked and not models:
                        complete = complete and "r50k_base" in plain and "p50k_base" in plain
            if complete:
                if models and name not in ("details", "controls"):
                    check(not any(value in plain for value in MODEL_NAMES),
                          f"{scene}: {name} exposed a raw encoding name")
                    check("F1" not in plain and "Enter newline" not in plain,
                          f"{scene}: {name} retained a removed footer hint")
                    if name in ("text", "caret"):
                        check("↓ options" in plain and "Tab switch" in plain,
                              f"{scene}: {name} omitted last-line navigation")
                if layout == "options" and name in ("text", "caret", "ids"):
                    check(re.search(r"\d+ tokens? · \d+ bytes?", plain) is None,
                          f"{scene}: {name} view exposed the advanced byte count")
                timestamp = float(event[0]) + 0.25
                if snapshots:
                    check(timestamp - snapshots[-1][1] > 0.5,
                          "editor: stages did not remain visible as separate frames")
                snapshots.append((name, timestamp))
                output = ""
    check(len(snapshots) == len(stages), "editor: recording omitted a required visible state")
    check(snapshots[-1][1] < float(events[-1][0]), "editor: snapshot occurs after recording ends")
    snapshot_name = "editor-snapshots.tsv" if scene == "after" else "before-editor-snapshots.tsv"
    (directory / snapshot_name).write_text(
        "".join(f"{name}\t{timestamp:.6f}\n" for name, timestamp in snapshots))
    print(f"PASS: {scene} {layout} editor views, exact details, tokenizer selection, controls, and Ctrl+C")


def main():
    check(len(sys.argv) == 3, "usage: validate.py CAPTURE_DIRECTORY MODE")
    validate(pathlib.Path(sys.argv[1]), sys.argv[2])


if __name__ == "__main__":
    main()
