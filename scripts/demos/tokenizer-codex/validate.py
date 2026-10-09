#!/usr/bin/env python3
"""Validate local utility transcripts and recorded process exit statuses."""

import json
import pathlib
import re
import sys
import textwrap


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
    "details": "$ openai tokenizer",
    "long-text": "$ openai tokenizer",
}
DETAILS_INPUTS = {"source": "a!b?c.", "ordinary": "how", "partial": " 👋", "overflow": " " * 128}
DETAILS_STAGES = ["source", "cursor", "up-navigation", "ordinary", "partial", "overflow-start", "overflow-end", "overflow-home", "recovery"]
CONTINUOUS_INPUTS = dict(DETAILS_INPUTS, source=" hello tokenization  ")
CONTINUOUS_STAGES = DETAILS_STAGES[:2] + ["trailing-space"] + DETAILS_STAGES[2:]
LONG_TAIL = " final-marker.  "
LONG_INPUTS = {
    "multiline": "\n".join(f"Line {line:03d}: café tokenization. End {line:03d}." for line in range(1, 121)),
    "long-line": ("alpha beta tokenization " * 512)[:8192 - len(LONG_TAIL)] + LONG_TAIL,
    "recovery": "how",
}
LONG_STAGES = {
    "legacy": ["multiline-end", "line-home", "line-end", "edited", "restored", "long-line-end", "long-line-home", "recovery"],
    "indexed": ["multiline-end", "document-home", "page-down", "page-up", "document-end", "edited", "restored", "long-line-end", "long-line-word-left", "long-line-word-right", "long-line-home", "recovery"],
}
CONFIG_URL = "https://learn.chatgpt.com/docs/config-file/config-basic"


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def validate(directory, mode):
    check(mode in COMMANDS, "MODE must be count, inspect, codex, guide, editor, details, or long-text")
    layouts = editor_layouts(directory) if mode == "editor" else {}
    before_status = 3 if mode == "guide" else 1
    if mode == "editor":
        before_status = 0 if layouts["before"] == "help" else 130
    if mode in ("details", "long-text"):
        before_status = 130
    after_status = 130 if mode in ("editor", "details", "long-text") else 0
    expected = [f"before\t{before_status}", f"after\t{after_status}"]
    if (directory / "expected-statuses.tsv").exists():
        expected = (directory / "expected-statuses.tsv").read_text().splitlines()
        check(len(expected) == 2 and expected[0].startswith("before\t") and
              expected[1] == f"after\t{after_status}", "invalid expected status record")
        before_status = int(expected[0].split("\t")[1])
        allowed = (0, 1, 3)
        if mode == "editor":
            allowed = (0,) if layouts["before"] == "help" else (130,)
        elif mode in ("details", "long-text"):
            allowed = (130,)
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
    if mode == "details":
        validate_details(directory, "before")
    elif mode == "long-text":
        validate_long_text(directory, "before")
    elif mode == "editor" and layouts["before"] != "help":
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
    elif mode == "details":
        validate_details(directory, "after")
    elif mode == "long-text":
        validate_long_text(directory, "after")
    else:
        validate_editor(directory, "after", layouts["after"])
    print(f"PASS: {mode} command transcripts and before/after exit statuses")


def details_expected_rows(kind, style, columns):
    """Exact, offline reference fixtures for the two supported capture widths."""
    width = columns - 4
    identifier, end, count = {"ordinary": (8923, 3, 1), "partial": (61138, 4, 2), "overflow": (72056, 128, 1)}[kind]
    raw_hex = {"ordinary": "68 6f 77", "partial": "20 f0 9f 91", "overflow": " ".join(["20"] * 128)}[kind]
    if style == "legacy":
        rows = [f"Token 1 of {count} · ID {identifier}", f"Encoding o200k_base · bytes [0, {end})"]
        if kind == "partial":
            rows += textwrap.wrap("Text: partial UTF-8; use the exact bytes below.", width)
        else:
            text = DETAILS_INPUTS[kind]
            rows += ["Text (escaped):"] + [text[i:i + width] for i in range(0, len(text), width)]
        rows += ["Hex:"]
        per_row = (width + 1) // 3
    else:
        value_width = width - 10
        value = "partial UTF-8; see Hex." if kind == "partial" else json.dumps(DETAILS_INPUTS[kind])
        parts = [value[i:i + value_width] for i in range(0, len(value), value_width)]
        rows = [("Text      " if i == 0 else " " * 10) + part for i, part in enumerate(parts)]
        rows += ["", f"Token ID  {identifier}", f"Bytes     [0, {end})", "Encoding  o200k_base"]
        per_row = (value_width + 1) // 3
    octets = raw_hex.split()
    for i in range(0, len(octets), per_row):
        prefix = ("Hex       " if i == 0 else " " * 10) if style == "refined" else ""
        rows.append(prefix + " ".join(octets[i:i + per_row]))
    return f"Token 1 of {count}", rows


def details_frame(plain, kind, style, columns):
    title, expected = details_expected_rows(kind, style, columns)
    if style == "legacy":
        title = "Token details · exact bytes"
    lines = [line.strip() for line in plain.splitlines()]
    if lines.count(title) != 1:
        return None
    lines = lines[lines.index(title) + 1:]
    footer = "↑↓ scroll · Esc back · Ctrl+C exit" if style == "legacy" else "Ctrl+C exit · Esc back"
    scrolling = style == "legacy" or kind == "overflow"
    if style == "refined" and scrolling:
        footer += " · ↑↓ scroll"
    if footer not in lines:
        return None
    body = lines[:lines.index(footer)]
    start, end, total = 1, len(expected), len(expected)
    if scrolling:
        if not body or not (counter := re.fullmatch(r"Rows (\d+)–(\d+) of (\d+)", body[-1])):
            return None
        start, end, total = map(int, counter.groups())
        body.pop()
    if style == "refined":
        if len(body) < 2 or body[0] != "" or body[-1] != "":
            return None
        body = body[1:-1]
    if total != len(expected) or not 1 <= start <= end <= total:
        return None
    if body != [row.strip() for row in expected[start - 1:end]]:
        return None
    return {"start": start, "end": end, "total": total}


def details_state(plain, state, style, columns, raw="", theme="no-color"):
    if state in ("source", "cursor", "up-navigation"):
        lines = [line.strip() for line in plain.splitlines()]
        if "6 tokens" not in lines or "Model  GPT-5.x & o1/o3  Default" not in plain:
            return False
        if state == "up-navigation":
            if style == "legacy":
                footer = "Ctrl+C exit · ←→ token · Enter details · Tab switch" if columns == 80 else "Ctrl+C exit · ←→ · Enter details"
                return "Token 2 of 6" in lines and '›["!"]' in plain and footer in lines
            footer = "Ctrl+C exit · ↑↓ move · Enter select" + (" · Tab switch" if columns == 80 else "")
            return "Token 3 of 6" in lines and any(line.startswith("› Model  ") for line in lines) and footer in lines
        if "Ctrl+C exit · ↓ options · Tab switch" not in lines:
            return False
        source = "a!b?c."
        if style == "legacy":
            source = "a!b?c.▏" if state == "source" else "a!b?c▏."
        elif ("\x1b[7m" + (" " if state == "source" else ".") + "\x1b[27m") not in raw:
            return False
        if "› Text " + source not in lines or "Token 6 of 6" not in lines or '·["."]' not in plain:
            return False
        if style == "refined":
            sgr = re.findall(r"\x1b\[([0-9;:]*)m", raw)
            if theme == "no-color":
                return all(code in ("", "0", "7", "27") for code in sgr)
            if columns == 80:
                backgrounds = {match.group(1) for code in sgr
                               for match in re.finditer(r"(?:^|;)48;2;(\d+;\d+;\d+)(?:;|$)", code)}
                return len(backgrounds) >= 6
        return True
    if state == "recovery":
        lines = [line.strip() for line in plain.splitlines()]
        return ("1 token" in lines and "Model  GPT-5.x & o1/o3  Default" in plain and
                '·["how"]' in plain and "Ctrl+C exit · ↓ options · Tab switch" in lines and "Esc back" not in plain)
    kind = state if state in ("ordinary", "partial") else "overflow"
    frame = details_frame(plain, kind, style, columns)
    if not frame:
        return False
    if state in ("overflow-start", "overflow-home"):
        return frame["start"] == 1 and frame["end"] < frame["total"]
    if state == "overflow-end":
        return frame["start"] > 1 and frame["end"] == frame["total"]
    return frame["start"] == 1 and frame["end"] == frame["total"]


def inline_rows(raw, columns):
    # The painter writes CHA + one wrap-cell space, then one next-row space + CR.
    # Remove only that explicit transport suffix, never the source's whitespace.
    boundary = f"\x1b[{columns}G "
    return raw.replace(boundary + " \r", "\r").replace(boundary + "\r", "\r").splitlines()


def source_at_cursor(raw, text, cursor, columns):
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
    expected = "  › Text " + text + (" " if cursor == len(text) else "")
    caret = "\x1b[7m" + (text[cursor:cursor + 1] or " ") + "\x1b[27m"
    return any(ansi.sub("", line) == expected and caret in line and
               ansi.sub("", line.split(caret, 1)[0]) == "  › Text " + text[:cursor]
               for line in inline_rows(raw, columns))


def continuous_state(plain, state, presentation, columns, raw, theme):
    """Version 6 retains exact source whitespace and complete selection spans."""
    if state not in ("source", "cursor", "trailing-space", "results", "up-navigation", "recovery"):
        return details_state(plain, state, "refined", columns, raw, theme)
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
    raw_lines = inline_rows(raw, columns)
    lines = [ansi.sub("", line) for line in raw_lines]
    stripped = [line.strip() for line in lines]
    recovery = state == "recovery"
    text = "how" if recovery else CONTINUOUS_INPUTS["source"]
    fragments = ["how"] if recovery else [" hello", " token", "ization", "  "]
    selected = 0 if recovery else 2 if state in ("cursor", "results", "up-navigation") else 3
    count = "1 token" if recovery else "4 tokens"
    if count not in stripped or "Updating" in plain or "Count unavailable" in plain:
        return False
    model = "Model  GPT-5.x & o1/o3  Default"
    if not any(line.removeprefix("› ") in (model, model + "  ›") for line in stripped):
        return False
    editing = state not in ("results", "up-navigation")
    footer = "Ctrl+C exit · ↓ options · Tab switch"
    if state == "results":
        footer = "Ctrl+C exit · ←→ token · Enter details · ↑ settings · Tab switch" if columns == 80 else "Ctrl+C exit · ←→ · Enter details"
    elif state == "up-navigation":
        footer = "Ctrl+C exit · ↑↓ move · Enter select" + (" · Tab switch" if columns == 80 else "")
        if not any(line.startswith("› Model  ") for line in stripped):
            return False
    if stripped.count(footer) != 1:
        return False
    cursor = len(text) if state in ("source", "recovery") else 13 if state == "cursor" else 20
    source = "  " + ("› Text " if editing else "  Text ") + text
    if editing and cursor == len(text):
        source += " "  # The EOF caret occupies its own display cell.
    if lines.count(source) != 1:
        return False
    if editing and not source_at_cursor(raw, text, cursor, columns):
        return False
    caption = f"Token {selected + 1} of {len(fragments)}"
    if state == "results" and presentation == "continuous":
        caption = "› " + caption
    if recovery:
        start = next(i for i, line in enumerate(stripped) if line.removeprefix("› ") in (model, model + "  ›")) + 1
    else:
        if stripped.count(caption) != 1:
            return False
        start = stripped.index(caption) + 1
    end = stripped.index(footer)
    result_raw = "\n".join(raw_lines[start:end])
    result_plain = "\n".join(lines[start:end])
    if presentation == "wrapped":
        first = 1 if columns == 40 and not recovery else 0
        expected = [("·" if editing and i == selected else "›" if state == "results" and i == selected else " ", value)
                    for i, value in enumerate(fragments) if i >= first]
        pattern = r'([ ·›])\["([^"\n]*)"\]'
        remainder = re.sub(pattern, "", result_plain).strip()
        if re.findall(pattern, result_plain) != expected or remainder != ("…" if first else ""):
            return False
    else:
        if [line for line in result_plain.splitlines() if line.strip()] != ["    " + text]:
            return False
        result_raw = next(line for line in result_raw.splitlines() if ansi.sub("", line).strip())
        if editing or state == "results":
            fragment = fragments[selected]
            position = 4 + sum(map(len, fragments[:selected]))
            if theme == "no-color":
                cue = "\x1b[4;7m" + fragment + "\x1b[24;27m"
                if cue not in result_raw or len(ansi.sub("", result_raw.split(cue, 1)[0])) != position:
                    return False
            else:
                colors = "38;2;16;19;24;48;2;138;168;255" if theme == "dark" else "38;2;255;255;255;48;2;49;89;188"
                cue = "\x1b[1;4m" + fragment + "\x1b[22;24m"
                if result_raw.count(cue) != 1:
                    return False
                prefix = result_raw.split(cue, 1)[0]
                if not prefix.endswith("\x1b[" + colors + "m") or len(ansi.sub("", prefix)) != position:
                    return False
        if theme != "no-color" and not recovery:
            backgrounds = set(re.findall(r"(?:^|;)48;2;(\d+;\d+;\d+)(?:;|$)", ";".join(re.findall(r"\x1b\[([0-9;]+)m", result_raw))))
            if len(backgrounds) != 4:
                return False
    if theme == "no-color":
        return all(code in ("", "0", "7", "27", "4;7", "24;27") for code in re.findall(r"\x1b\[([0-9;:]*)m", raw))
    return True


def validate_details(directory, scene):
    report_name = "before-editor-input.json" if scene == "before" else "editor-input.json"
    report = json.loads((directory / report_name).read_text())
    presentation_path = directory / "details-presentation.txt"
    presentation = presentation_path.read_text().strip() if presentation_path.exists() else "historical"
    check(presentation in ("historical", "continuous"), "details: unknown requested presentation")
    continuous = presentation == "continuous"
    style = "legacy" if scene == "before" and not continuous else "refined"
    tokens = "wrapped" if scene == "before" else "continuous"
    inputs = CONTINUOUS_INPUTS if continuous else DETAILS_INPUTS
    stages = CONTINUOUS_STAGES if continuous else DETAILS_STAGES
    check(report.get("capture_version") == (6 if continuous else 5) and report.get("details_style") == style,
          "details: wrong capture version or presentation")
    if continuous:
        check(report.get("token_presentation") == tokens, "details: wrong token presentation")
    check(report.get("inputs") == inputs and report.get("exit_status") == 130,
          "details: fixtures or exit status changed")
    check(report.get("columns") in (40, 80) and report.get("rows") == 12, "details: dimensions changed")
    check(report.get("theme") in ("dark", "light", "no-color"), "details: unknown color policy")
    check([event["state"] for event in report["states"]] == stages, "details: incomplete driver states")
    events = [json.loads(line) for line in (directory / f"{scene}.cast").read_text().splitlines()]
    check(events[0]["width"] == report["columns"] and events[0]["height"] == 12, "details: cast dimensions changed")
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
    output, snapshots, covered = "", [], set()
    for event in events[1:]:
        if event[1] != "o":
            continue
        output = (output + event[2])[-65536:]
        if "\r\x1b[J" not in output:
            continue
        frame = output.rsplit("\r\x1b[J", 1)[1]
        plain = ansi.sub("", frame)
        if overflow := details_frame(plain, "overflow", style, report["columns"]):
            covered.update(range(overflow["start"], overflow["end"] + 1))
        if len(snapshots) == len(stages):
            continue
        state = stages[len(snapshots)]
        matched = (continuous_state(plain, state, tokens, report["columns"], frame, report["theme"]) if continuous else
                   details_state(plain, state, style, report["columns"], frame, report["theme"]))
        if matched:
            if state == "overflow-end":
                total = len(details_expected_rows("overflow", style, report["columns"])[1])
                check(covered == set(range(1, total + 1)), "details: overflow omitted exact data rows")
            timestamp = float(event[0]) + .25
            check(not snapshots or timestamp - snapshots[-1][1] > .5, "details: stages were not held separately")
            snapshots.append((state, timestamp))
            output = ""
    check(len(snapshots) == len(stages), "details: omitted an exact detail or recovery state")
    check(snapshots[-1][1] < float(events[-1][0]), "details: snapshot exceeds the recording")
    name = "before-editor-snapshots.tsv" if scene == "before" else "editor-snapshots.tsv"
    (directory / name).write_text("".join(f"{state}\t{stamp:.6f}\n" for state, stamp in snapshots))
    print(f"PASS: {scene} ordinary, partial UTF-8, complete overflow bytes, Home, and editor recovery")


def long_text_spec(state, columns):
    if state.startswith("long-line"):
        text = LONG_INPUTS["long-line"]
        if state == "long-line-word-left":
            return text, 1367, 1, len(text) - len("final-marker.  "), 1364, " final"
        home = state == "long-line-home"
        return text, 1367, 1, 0 if home else len(text), 1 if home else 1367, "alpha" if home else "  "
    if state == "recovery":
        return "how", 1, 1, 3, 1, "how"
    text = LONG_INPUTS["multiline"] + ("!" if state == "edited" else "")
    line = 120
    if state in ("document-home", "page-up"):
        line = 1
    elif state == "page-down":
        line = 4 if columns == 80 else 2
    home = state in ("line-home", "document-home", "page-down", "page-up")
    column = 0 if home else len(text.split("\n")[line - 1])
    return text, 1440, line, column, (line - 1) * 12 + 1 if home else 1440, "Line" if home else ".!" if state == "edited" else "."


def long_source_excerpt(text, cursor, width):
    # Fixtures use single-cell graphemes, including café's precomposed é.
    start = max(0, cursor - max(0, width - 3)) if cursor >= 0 else 0
    value, used = ("…", 1) if start else ("", 0)
    for index in range(start, len(text)):
        if used + 1 > width - 1:
            return value + ("\x1b[7m…\x1b[27m" if index == cursor else "…")
        value += "\x1b[7m" + text[index] + "\x1b[27m" if index == cursor else text[index]
        used += 1
    if cursor == len(text):
        value += "\x1b[7m \x1b[27m"
    return value


def long_text_state(raw, state, style, columns, theme):
    ansi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
    rows = inline_rows(raw, columns)
    plain = [ansi.sub("", row) for row in rows]
    stripped = [row.strip() for row in plain]
    text, count, line, column, position, fragment = long_text_spec(state, columns)
    logical = text.split("\n")
    source_rows = 3 if columns == 80 else 1
    overflow = len(logical) > source_rows
    caption = "Text" + (f" · {line}/{len(logical)}" if overflow and style == "indexed" else "")
    if columns == 80:
        border = "╭─ " + caption + " "
        border = "  " + border + "─" * (columns - 5 - len(border)) + "╮"
        if plain.count(border) != 1:
            return False
        source_start = plain.index(border) + 1
        first = max(0, line - 2)
        expected_rows = []
        for index in range(first, first + 3):
            value = logical[index] if index < len(logical) else ""
            excerpt = long_source_excerpt(value, column if index == line - 1 else -1, columns - 8)
            padding = " " * (columns - 8 - len(ansi.sub("", excerpt)))
            expected_rows.append("  │ " + excerpt + padding + " │")
    else:
        prefix = "  › " + caption + " "
        excerpt = long_source_excerpt(logical[line - 1], column, columns - 4 - len(prefix) + 2)
        expected_rows = [prefix + excerpt]
        candidates = [index for index, row in enumerate(plain) if row.startswith(prefix)]
        if len(candidates) != 1:
            return False
        source_start = candidates[0]
    actual_rows = rows[source_start:source_start + len(expected_rows)]
    if len(actual_rows) != len(expected_rows):
        return False
    for row, expected in zip(actual_rows, expected_rows):
        expected_plain = ansi.sub("", expected)
        if ansi.sub("", row) != expected_plain:
            return False
        if "\x1b[7m" in expected:
            caret = re.search(r"\x1b\[7m([^\x1b]*)\x1b\[27m", expected)
            if not caret or caret[0] not in row:
                return False
            if ansi.sub("", row.split(caret[0], 1)[0]) != ansi.sub("", expected.split(caret[0], 1)[0]):
                return False
    if style == "legacy" and any("Text · " in row for row in plain):
        return False
    if not overflow and any("Text · " in row for row in plain):
        return False
    count_row = f"{count} token" + ("s" if count != 1 else "")
    if stripped.count(count_row) != 1 or any("Updating" in row or "Count unavailable" in row for row in plain):
        return False
    model = "Model  GPT-5.x & o1/o3  Default"
    if not any(row in (model, model + "  ›") for row in stripped):
        return False
    footer = "Ctrl+C exit · ↓ options · Tab switch" if line == len(logical) else "Ctrl+C exit · Tab options"
    if overflow and style == "indexed" and len(footer + " · PgUp/PgDn") <= columns - 4:
        footer += " · PgUp/PgDn"
    if stripped.count(footer) != 1:
        return False
    if count > 1:
        heading = f"Token {position} of {count}"
        if stripped.count(heading) != 1:
            return False
        start = stripped.index(heading) + 1
    else:
        start = next(index for index, row in enumerate(stripped) if row in (model, model + "  ›")) + 1
        if any(re.fullmatch(r"(?:› )?Token \d+ of \d+", row) for row in stripped):
            return False
    result = "\n".join(rows[start:stripped.index(footer)])
    if theme == "no-color":
        cue = "\x1b[4;7m" + fragment + "\x1b[24;27m"
        if result.count(cue) != 1:
            return False
        return all(code in ("", "0", "7", "27", "4;7", "24;27") for code in re.findall(r"\x1b\[([0-9;:]*)m", raw))
    colors = "38;2;16;19;24;48;2;138;168;255" if theme == "dark" else "38;2;255;255;255;48;2;49;89;188"
    cue = "\x1b[" + colors + "m\x1b[1;4m" + fragment + "\x1b[22;24m"
    return result.count(cue) == 1


def validate_long_text(directory, scene):
    report_name = "before-editor-input.json" if scene == "before" else "editor-input.json"
    report = json.loads((directory / report_name).read_text())
    style = "legacy" if scene == "before" else "indexed"
    stages = LONG_STAGES[style]
    check(report.get("capture_version") == 7 and report.get("navigation") == style, "long-text: wrong capture version or navigation")
    check(report.get("inputs") == LONG_INPUTS and report.get("exit_status") == 130, "long-text: input or status changed")
    check((report.get("columns"), report.get("rows")) in ((80, 20), (40, 12)), "long-text: dimensions changed")
    check(report.get("theme") in ("dark", "light", "no-color"), "long-text: unknown color policy")
    check([event["state"] for event in report["states"]] == stages, "long-text: missing navigation or edit stage")
    events = [json.loads(line) for line in (directory / f"{scene}.cast").read_text().splitlines()]
    check((events[0]["width"], events[0]["height"]) == (report["columns"], report["rows"]), "long-text: cast dimensions changed")
    output, snapshots = "", []
    for event in events[1:]:
        if event[1] != "o":
            continue
        output = (output + event[2])[-65536:]
        if "\r\x1b[J" not in output or len(snapshots) == len(stages):
            continue
        frame = output.rsplit("\r\x1b[J", 1)[1]
        state = stages[len(snapshots)]
        if long_text_state(frame, state, style, report["columns"], report["theme"]):
            stamp = float(event[0]) + .25
            check(not snapshots or stamp - snapshots[-1][1] > .5, "long-text: stages were not held separately")
            snapshots.append((state, stamp))
            output = ""
    check(len(snapshots) == len(stages), "long-text: missing exact navigation, edit, or recovery frame")
    check(snapshots[-1][1] < float(events[-1][0]), "long-text: snapshot exceeds recording")
    name = "before-editor-snapshots.tsv" if scene == "before" else "editor-snapshots.tsv"
    (directory / name).write_text("".join(f"{state}\t{stamp:.6f}\n" for state, stamp in snapshots))
    print(f"PASS: {scene} exact long-text source, navigation, edit restoration, and recovery")


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
