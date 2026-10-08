#!/usr/bin/env python3
"""Check automatic pages and printed IDs through the existing PTY harness."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
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


def screen_text(terminal):
    # Reconstruct cursor-addressed frames. ANSI stripping cannot prove current contents.
    with tempfile.TemporaryDirectory(prefix="list-table-screen-") as temporary:
        path = Path(temporary) / "screen.cast"
        header = {"version": 2, "width": terminal.initial_size[0], "height": terminal.initial_size[1]}
        path.write_text("\n".join(json.dumps(value) for value in [header, *terminal.events]) + "\n")
        result = subprocess.run(["asciinema", "convert", "-f", "txt", str(path), "-"], capture_output=True,
                                text=True, check=True, env={"PATH": os.environ.get("PATH", "/usr/bin:/bin"),
                                "ASCIINEMA_CONFIG_HOME": temporary, "ASCIINEMA_STATE_HOME": temporary})
    return result.stdout


def wait_screen(terminal, marker, *, absent=None, after_event=-1):
    deadline = time.monotonic() + 12
    while True:
        terminal.read()
        text = screen_text(terminal)
        fresh_output = any(event[1] == "o" for event in terminal.events[after_event + 1:])
        if fresh_output and marker in text and (absent is None or absent not in text):
            return text
        assert terminal.child.poll() is None and time.monotonic() < deadline, ("missing loaded marker", marker, text)


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
        for scenario in ("long-id", "empty", "controls", "unknown", "error"):
            name = f"{resource}-{scenario}"
            env = {"PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
                   "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_ADMIN_KEY": "synthetic-demo-admin-key",
                   "OPENAI_BASE_URL": f"{args.api_url}/{scenario}/v1", "NO_COLOR": "1", "FORCE_COLOR": "0",
                   "GOMAXPROCS": "2"}
            start = len(requests.read_text().splitlines())
            terminal = harness.Terminal(args.binary, COMMANDS[resource], env, width=args.width, height=48)
            expected_ids = IDS[resource]
            if scenario == "long-id":
                prefix = {"files": "file-", "batches": "batch-", "projects": "proj-"}[resource]
                expected_ids = [prefix + "x" * (77 - len(prefix) - 3) + "-01"]
            try:
                if scenario == "error":
                    terminal.finish(1)
                    printed = bytes(terminal.raw)
                else:
                    marker = "p001" if scenario == "long-id" else "No results." if scenario == "empty" else "20261007"
                    if scenario == "long-id" and resource == "batches":
                        marker = "completed"
                    wait_screen(terminal, marker)
                    wait_screen(terminal, "p: print page, quit")
                    if scenario == "long-id":
                        mark = len(terminal.events) - 1
                        terminal.resize(20, 48)
                        wait_screen(terminal, "Space: more q: quit", absent="Space: more   q: quit", after_event=mark)
                        mark = len(terminal.events) - 1
                        terminal.resize(args.width, 48)
                        wait_screen(terminal, "Space: more   q: quit", absent="Space: more q: quit", after_event=mark)
                        assert len(requests.read_text().splitlines()) - start == 1, "resize fetched another page"
                    if scenario == "unknown":
                        assert "detail-002" in screen_text(terminal), "viewport omitted unfamiliar data"
                    assert terminal.child.poll() is None, "viewer exited before key delivery"
                    after_key = len(terminal.raw)
                    assert os.write(terminal.master, b"q" if scenario == "empty" else b"p") == 1, "short key write"
                    terminal.finish(0)
                    marker = b"\x1b[?2004l"
                    following_key = bytes(terminal.raw)[after_key:]
                    assert marker in following_key, "terminal input was not restored after key delivery"
                    printed = following_key.rsplit(marker, 1)[-1]
                    if scenario != "empty":
                        plain_printed = harness.ANSI.sub("", printed.decode("utf-8", "replace"))
                        identifiers = re.findall(r"(?:^|[\r\n])ID: ([^\r\n]+)\r?\n", plain_printed)
                        for identifier in expected_ids:
                            assert ("ID: " + identifier + "\r\n").encode() in printed, "print inserted an ID newline"
                        assert identifiers == expected_ids, (name, "printed IDs changed", identifiers)
                        assert b"p: print page" not in printed, "another viewport frame followed restoration"
                if scenario == "empty":
                    assert b"No results." in terminal.raw, "empty-page message missing"
                if scenario == "controls":
                    assert b"demo-\x1b[31mred" not in terminal.raw, "API text injected terminal controls"
                    assert "\u202e".encode() not in terminal.raw, "API text retained a bidi control"
                    assert b"\\u001b" in printed, "escaped API control is missing"
                if scenario == "unknown":
                    assert b"detail-002" in printed, "unfamiliar data disappeared"
                events = [json.loads(line) for line in requests.read_text().splitlines()[start:]]
                assert events == [{"scenario": scenario, "resource": route, "page": 1,
                                   "status": 400 if scenario == "error" else 200, "cursor_valid": True}], events
                checks.append({"resource": resource, "scenario": scenario, "width": args.width, "result": "PASS"})
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
