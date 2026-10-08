#!/usr/bin/env python3
"""Check automatic pages and printed IDs through the existing PTY harness."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import time

sys.dont_write_bytecode = True
source = Path(__file__).resolve()
spec = importlib.util.spec_from_file_location("list_table_data", source.with_name("validate.py"))
comparison = importlib.util.module_from_spec(spec)
spec.loader.exec_module(comparison)
COMMANDS, IDS = comparison.COMMANDS, comparison.IDS
spec = importlib.util.spec_from_file_location("list_table_terminal", source.parents[2] / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
spec = importlib.util.spec_from_file_location("list_table_navigation", source.parents[2] / "check-list-navigation.py")
navigation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(navigation)
screen_text = navigation.screen_text


def wait_screen(terminal, marker, *, absent=None, after_event=-1):
    deadline = time.monotonic() + 12
    while True:
        terminal.read()
        text = screen_text(terminal)
        fresh_output = any(event[1] == "o" for event in terminal.events[after_event + 1:])
        if fresh_output and marker in text and (absent is None or absent not in text):
            return text
        assert terminal.child.poll() is None and time.monotonic() < deadline, ("missing loaded marker", marker, text)


def send_key(terminal, key, keys):
    assert os.write(terminal.master, key.encode()) == 1, "short key write"
    keys.append(key)
    terminal.events.append([time.monotonic() - terminal.started, "i", key])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary")
    parser.add_argument("api_url")
    parser.add_argument("output_dir")
    parser.add_argument("--width", type=int, choices=(40, 110), required=True)
    parser.add_argument("--resource", choices=tuple(COMMANDS))
    args = parser.parse_args()
    output = Path(args.output_dir)
    destination = output / "page-checks"
    destination.mkdir()
    requests = output / "requests.jsonl"
    checks = []
    for resource in [args.resource] if args.resource else COMMANDS:
        route = "organization/projects" if resource == "projects" else resource
        completed_rows = {}
        for scenario, mode in (("long-id", "print"), ("empty", "complete"),
                               ("controls", "complete"), ("controls", "print"),
                               ("unknown", "complete"), ("unknown", "print"), ("error", "error")):
            name = f"{resource}-{scenario}-{mode}"
            env = {"PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
                   "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_ADMIN_KEY": "synthetic-demo-admin-key",
                   "OPENAI_BASE_URL": f"{args.api_url}/{scenario}/v1", "NO_COLOR": "1", "FORCE_COLOR": "0",
                   "GOMAXPROCS": "2"}
            start = len(requests.read_text().splitlines())
            height = completed_rows[scenario] if mode == "print" and scenario != "long-id" else 48
            terminal = harness.Terminal(args.binary, COMMANDS[resource], env, width=args.width, height=height)
            expected_ids = IDS[resource]
            keys = []
            if scenario == "long-id":
                prefix = {"files": "file-", "batches": "batch-", "projects": "proj-"}[resource]
                expected_ids = [prefix + "x" * (77 - len(prefix) - 3) + "-01"]
            try:
                if scenario == "error":
                    navigation.finish(terminal, 1)
                    printed = bytes(terminal.raw)
                elif mode == "complete":
                    text = navigation.completed_output(terminal, [] if scenario == "empty" else expected_ids)
                    printed = bytes(terminal.raw).rsplit(b"\x1b[?2004l", 1)[-1]
                    completed_rows[scenario] = navigation.physical_rows(text, args.width)
                    if scenario == "empty":
                        assert text == "No results.\n", ("empty output changed", text)
                        assert terminal.raw.count(b"No results.") == 1, "empty result was rendered twice"
                    else:
                        assert completed_rows[scenario] > 2, "fixture cannot exercise height overflow"
                else:
                    marker = "p001" if scenario == "long-id" else expected_ids[0]
                    if scenario == "long-id" and resource == "batches":
                        marker = "completed"
                    wait_screen(terminal, marker)
                    wait_screen(terminal, "p: print page, quit")
                    if scenario == "long-id":
                        mark = len(terminal.events) - 1
                        terminal.resize(20, height)
                        wait_screen(terminal, navigation.navigation_footer(20),
                                    absent=navigation.navigation_footer(args.width), after_event=mark)
                        mark = len(terminal.events) - 1
                        terminal.resize(args.width, height)
                        wait_screen(terminal, navigation.navigation_footer(args.width),
                                    absent=navigation.navigation_footer(20), after_event=mark)
                        assert len(requests.read_text().splitlines()) - start == 1, "resize fetched another page"
                    else:
                        wait_screen(terminal, navigation.navigation_footer(args.width))
                        assert "End of results" not in screen_text(terminal), "overflow page falsely indicated its end"
                        if scenario == "unknown":
                            send_key(terminal, " ", keys)
                            wait_screen(terminal, "detail-002")
                            wait_screen(terminal, navigation.navigation_footer(args.width, complete=True))
                    assert terminal.child.poll() is None, "viewer exited before key delivery"
                    after_key = len(terminal.raw)
                    send_key(terminal, "p", keys)
                    navigation.finish(terminal, 0)
                    marker = b"\x1b[?2004l"
                    following_key = bytes(terminal.raw)[after_key:]
                    assert marker in following_key, "terminal input was not restored after key delivery"
                    printed = following_key.rsplit(marker, 1)[-1]
                    plain_printed = harness.ANSI.sub("", printed.decode("utf-8", "replace"))
                    identifiers = re.findall(r"(?:^|[\r\n])ID: ([^\r\n]+)\r?\n", plain_printed)
                    for identifier in expected_ids:
                        assert ("ID: " + identifier + "\r\n").encode() in printed, "print inserted an ID newline"
                    assert identifiers == expected_ids, (name, "printed IDs changed", identifiers)
                    assert b"p: print page" not in printed, "another viewport frame followed restoration"
                if scenario == "controls":
                    assert b"demo-\x1b[31mred" not in terminal.raw, "API text injected terminal controls"
                    assert "\u202e".encode() not in terminal.raw, "API text retained a bidi control"
                    # Automatic tables can truncate cells. Printing must retain the full escaped value.
                    if mode == "print":
                        assert b"\\u001b" in printed, "escaped API control is missing"
                if scenario == "unknown":
                    assert b"detail-002" in printed, "unfamiliar data disappeared"
                assert not re.search(rb"\x1b\[[23]J", terminal.raw), "command cleared the screen or scrollback"
                expected_keys = [" ", "p"] if scenario == "unknown" else ["p"]
                assert keys == (expected_keys if mode == "print" else []), (name, "unexpected key delivery", keys)
                (destination / f"{name}.printed.raw").write_bytes(printed)
                events = [json.loads(line) for line in requests.read_text().splitlines()[start:]]
                assert events == [{"scenario": scenario, "resource": route, "page": 1,
                                   "status": 400 if scenario == "error" else 200, "cursor_valid": True}], events
                checks.append({"resource": resource, "scenario": scenario, "mode": mode, "keys": keys,
                               "width": args.width, "height": height, "result": "PASS"})
                print(f"PASS {name}: automatic terminal output; one request; expected exit status")
            finally:
                terminal.save(destination / name)
                recording = destination / f"{name}.cast"
                lines = recording.read_text().splitlines()
                header = json.loads(lines[0])
                header["title"] = f"List tables: {name}; synthetic API"
                recording.write_text("\n".join([json.dumps(header), *lines[1:]]) + "\n")
                (destination / f"{name}.requests.json").write_text(json.dumps(
                    [json.loads(line) for line in requests.read_text().splitlines()[start:]], indent=2) + "\n")
                terminal.close()
    (destination / "results.json").write_text(json.dumps(checks, indent=2) + "\n")
    print(f"PASS {len(checks)} automatic-page checks")


if __name__ == "__main__":
    main()
