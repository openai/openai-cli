#!/usr/bin/env python3
"""Check pager results and complete IDs through the existing PTY harness."""

import argparse
import importlib.util
import json
import os
from pathlib import Path
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
spec = importlib.util.spec_from_file_location("list_table_navigation", source.parents[2] / "check-list-navigation.py")
navigation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(navigation)


def send_key(terminal, key, keys):
    written = os.write(terminal.master, key.encode())
    if written != 1:
        raise AssertionError("short key write")
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
    scenarios = (("long-id", "quit", 48), ("empty", "complete", 48),
                 ("controls", "complete", 48), ("controls", "chunks", 3),
                 ("unknown", "complete", 48), ("unknown", "chunks", 3),
                 ("error", "error", 48), ("multipage", "advance", 48),
                 ("partial", "error-next", 48))
    for resource in [args.resource] if args.resource else COMMANDS:
        route = "organization/projects" if resource == "projects" else resource
        for scenario, mode, height in scenarios:
            name = f"{resource}-{scenario}-{mode}"
            with tempfile.TemporaryDirectory(prefix="list-table-pages-") as home:
                env = {"PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
                       "TERM_PROGRAM": "Apple_Terminal", "HOME": home, "XDG_CONFIG_HOME": home,
                       "OPENAI_API_KEY": "synthetic-demo-key", "OPENAI_ADMIN_KEY": "synthetic-demo-admin-key",
                       "OPENAI_BASE_URL": f"{args.api_url}/{scenario}/v1", "NO_COLOR": "1", "FORCE_COLOR": "0",
                       "OPENAI_CLI_NO_UPDATE_CHECK": "1", "OPENAI_CLI_NO_AUTO_UPDATE": "1", "GOMAXPROCS": "2"}
                start = len(requests.read_text().splitlines())
                terminal = harness.Terminal(args.binary, COMMANDS[resource], env, width=args.width, height=height)
                expected_ids = IDS[resource]
                keys = []
                expected_status = 1 if scenario in {"error", "partial"} else 0
                expected_count = 2 if mode in {"advance", "error-next"} else 1
                if scenario == "empty" or scenario == "error":
                    expected_ids = []
                elif scenario == "partial":
                    expected_ids = expected_ids[:1]
                elif scenario == "long-id":
                    prefix = {"files": "file-", "batches": "batch-", "projects": "proj-"}[resource]
                    expected_ids = [prefix + "x" * (77 - len(prefix) - 3) + "-01"]
                views = []

                def visible():
                    navigation.drain(terminal, 0.05)
                    return navigation.screen_text(terminal)

                def wait_view(marker=None, previous=None):
                    deadline = time.monotonic() + 10
                    while True:
                        frame = visible()
                        compact = ''.join(frame.splitlines())
                        if "q: quit" in frame and (marker is None or marker in compact) and frame != previous:
                            views.append(frame)
                            return frame
                        assert terminal.child.poll() is None, terminal.text()[-1500:]
                        assert time.monotonic() < deadline, ("pager frame missing", marker, frame)
                        terminal.read()

                try:
                    if mode != "error":
                        frame = wait_view(expected_ids[0] if expected_ids else "No results.")
                        assert len(requests.read_text().splitlines()) - start == 1, "idle pager fetched another page"
                        if mode == "quit":
                            terminal.resize(20, height)
                            navigation.drain(terminal)
                            terminal.resize(args.width, height)
                            wait_view(expected_ids[0])
                            assert len(requests.read_text().splitlines()) - start == 1, "resize fetched another page"
                        elif mode in {"advance", "error-next"}:
                            send_key(terminal, " ", keys)
                            if mode == "advance":
                                wait_view(expected_ids[-1])
                                send_key(terminal, "b", keys)
                                wait_view(expected_ids[0])
                                send_key(terminal, " ", keys)
                                wait_view(expected_ids[-1])
                        elif mode == "chunks":
                            for _ in range(100):
                                if "Space: more" not in frame:
                                    break
                                previous = frame
                                send_key(terminal, " ", keys)
                                frame = wait_view(previous=previous)
                                assert len(requests.read_text().splitlines()) - start == 1, "loaded chunks fetched another page"
                            else:
                                raise AssertionError("loaded chunks exceeded fixture bound")
                            assert keys, "small terminal did not exercise loaded chunks"
                        if mode != "error-next":
                            assert terminal.child.poll() is None, "final page did not remain open"
                            send_key(terminal, "q", keys)
                    navigation.finish(terminal, expected_status)
                    final = navigation.screen_text(terminal)
                    text = '\n'.join(views)
                    compact = ''.join(text.splitlines())
                    for identifier in expected_ids:
                        assert identifier in compact, ("full ID missing from pager", identifier, text)
                        if expected_status == 0:
                            assert identifier not in final, ("quit dumped records", final)
                        else:
                            assert final.count(identifier) == 1, ("partial results changed", final)
                    printed = bytes(terminal.raw)
                    if scenario == "empty":
                        assert "No results." in text, text
                    if scenario == "controls":
                        assert b"demo-\x1b[31mred" not in printed, "API text injected terminal controls"
                        assert "\u202e".encode() not in printed, "API text retained a bidi control"
                        assert b"demo-\\u" in printed, "escaped API control prefix is missing"
                    if scenario == "unknown":
                        assert "detail-002" in text, "unfamiliar data disappeared"
                    if scenario in {"error", "partial"}:
                        assert b"HTTP 400: Bad Request." in printed, "API status disappeared"
                        assert b"API error details: --format-error json." in printed, "API error guidance disappeared"
                    (destination / f"{name}.raw").write_bytes(printed)
                    (destination / f"{name}.views.txt").write_text(text)
                    (destination / f"{name}.final.txt").write_text(final)
                    events = [json.loads(line) for line in requests.read_text().splitlines()[start:]]
                    expected = [{"scenario": scenario, "resource": route, "page": page,
                                 "status": 400 if scenario == "error" or scenario == "partial" and page == 2 else 200,
                                 "cursor_valid": True} for page in range(1, expected_count + 1)]
                    assert events == expected, (events, expected)
                    checks.append({"resource": resource, "scenario": scenario, "mode": mode, "keys": keys,
                                   "width": args.width, "height": height, "result": "PASS"})
                    print(f"PASS {name}: pager output; {expected_count} request(s); status {expected_status}")
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
    print(f"PASS {len(checks)} pager checks")


if __name__ == "__main__":
    main()
