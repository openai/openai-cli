#!/usr/bin/env python3
"""Probe Models pager navigation without live credentials or private model records.

Usage: python3 -I -B scripts/check-models-list-viewport.py BINARY OUTPUT
The default fixture contains 11,892 synthetic records in one HTTP response.
"""
import argparse
import functools
import hashlib
import http.server
import importlib.util
import json
import pathlib
import platform
import re
import subprocess
import tempfile
import threading
import time

spec = importlib.util.spec_from_file_location(
    "models_terminal", pathlib.Path(__file__).with_name("image_picker_harness.py"))
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
spec = importlib.util.spec_from_file_location(
    "models_navigation", pathlib.Path(__file__).with_name("check-list-navigation.py"))
navigation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(navigation)


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        self.server.requests.append({"path": self.path,
                                     "at_seconds": time.monotonic() - self.server.started})
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(self.server.body)))
        self.end_headers()
        try:
            self.wfile.write(self.server.body)
        except (BrokenPipeError, ConnectionResetError):
            # The CLI can disconnect when canceled or when the test closes its terminal.
            pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("output")
    parser.add_argument("--expect-viewer", action="store_true",
                        help="Compatibility option; pager checks always run")
    parser.add_argument("--expect-id-owner", "--expect-names", dest="expect_id_owner", action="store_true",
                        help="Require exact IDs and owners without unrelated metadata")
    parser.add_argument("--count", type=int, default=11892)
    parser.add_argument("--width", type=int, default=110)
    parser.add_argument("--height", type=int, default=30)
    parser.add_argument("--table", action="store_true", help="Use short IDs and supported table fields")
    parser.add_argument("--asciinema", default="asciinema")
    args = parser.parse_args()
    navigation.ASCIINEMA = args.asciinema
    binary, output = pathlib.Path(args.binary).resolve(), pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    items = [{"id": f"synthetic-standard-model-for-viewport-regression-with-long-readable-id-{i:05d}",
              "object": "model", "created": 1700000000, "owned_by": "synthetic-owner",
              "shutdown_date": "2030-01-01" if i % 2 else None} for i in range(args.count)]
    if args.table:
        items = [{"id": f"synthetic-model-{i:05d}", "object": "model",
                  "created": 1700000000, "owned_by": "synthetic-owner"} for i in range(args.count)]
    for index, item in enumerate(items):
        item["owned_by"] = f"synthetic-owner-{index:05d}"
    # Put equal IDs on opposite sides of the first page boundary. Distinct owners
    # expose unstable sorting and prevent a duplicated row from hiding a missing one.
    if len(items) > 100:
        items[100]["id"] = items[99]["id"]
    response_items = items[::2][::-1] + items[1::2]
    ordered = sorted(response_items, key=lambda item: item["id"])
    pages = [ordered[start:start + 100] for start in range(0, len(ordered), 100)] or [[]]
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    server.body = json.dumps({"object": "list", "data": response_items}, separators=(",", ":")).encode()
    server.requests, server.started = [], time.monotonic()
    thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02}, daemon=True)
    thread.start()
    result = {"binary": str(binary), "sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "models": args.count, "response_bytes": len(server.body), "width": args.width,
              "height": args.height, "term": "xterm-256color", "ci": "unset", "pager": "cat",
              "fixture": "synthetic only; no private model IDs", "arguments": ["models", "list"],
              "platform": platform.platform(), "stdin": "private PTY", "stdout": "private PTY",
              "stderr": "same private PTY", "table": args.table}
    with tempfile.TemporaryDirectory(prefix="models-viewport-") as temporary:
        env = {"PATH": "/usr/bin:/bin", "HOME": temporary, "TERM": "xterm-256color",
               "LANG": "en_US.UTF-8", "NO_COLOR": "1", "PAGER": "cat",
               "GOMAXPROCS": "2",
               "OPENAI_API_KEY": "synthetic-models-viewport-key",
               "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1"}
        result["version"] = subprocess.run([str(binary), "--version"], env=env,
                                           capture_output=True, text=True, check=True).stdout.strip()
        terminal = harness.Terminal(str(binary), ["models", "list"], env,
                                    width=args.width, height=args.height)
        try:
            @functools.cache
            def expected_rows(width):
                if not ordered:
                    return "", [], []
                id_width = max(len("ID"), *(len(item["id"]) for item in ordered))
                owner_width = max(len("OWNER"), *(len(item["owned_by"]) for item in ordered))
                table = id_width + 2 + owner_width <= width
                header = "ID".ljust(id_width) + "  OWNER" if table else ""
                rows, ordinals = [], []
                for index, item in enumerate(ordered, 1):
                    if table:
                        record = [item["id"].ljust(id_width) + "  " + item["owned_by"]]
                    else:
                        record = ["ID: " + item["id"], "Owned by: " + item["owned_by"]]
                    for line in record:
                        wrapped = [line[i:i + width].rstrip() for i in range(0, len(line), width)]
                        rows.extend(wrapped)
                        ordinals.extend([index] * len(wrapped))
                return header, rows, ordinals

            def screen(name=None, offset=None):
                assert terminal.child.poll() is None, "Pager exited before q"
                value = navigation.screen_text(terminal)
                if name is not None:
                    (output / (name + ".txt")).write_text(value)
                assert len(server.requests) == 1, "Paging fetched another API response"
                width, height = terminal.size()
                lines = [line.rstrip() for line in value.rstrip("\n").splitlines()]
                assert lines and len(lines) <= height, "Empty or oversized pager screen"
                footer = lines.pop()
                assert "q" in footer, "Missing quit control"
                if not ordered:
                    assert lines == ["No results."] and "End" in footer
                    return value, []
                if width >= len("Details: --format json") and height >= 4:
                    assert lines.pop() == "Details: --format json"
                header, rows, ordinals = expected_rows(width)
                if header:
                    assert lines.pop(0) == header, "Table header must remain pinned"
                if offset is not None:
                    end = offset + len(lines)
                    assert lines and lines == rows[offset:end], f"Model rows changed at offset {offset}: {lines!r}"
                    first, last = ordinals[offset], ordinals[end-1]
                    expected_range = f"Model {first}" if first == last else f"Models {first}–{last}"
                    if width >= 55:
                        assert expected_range + f" of {len(ordered)}" in footer, footer
                    if width >= 25:
                        assert ("End" in footer) == (end == len(rows)), (end, footer)
                return value, lines

            id_pattern = re.compile(
                r"(?:synthetic-standard-model-for-viewport-regression-with-long-readable-id-|"
                r"synthetic-model-)\d{5,}(?!\d)")

            def change_view(key):
                offset = len(terminal.raw)
                terminal.send(key)
                deadline = time.monotonic() + 8
                while len(terminal.raw) == offset:
                    terminal.read(0.02)
                    assert terminal.child.poll() is None, "Navigation dismissed the pager"
                    assert time.monotonic() < deadline, "Navigation did not update the screen"
                navigation.drain(terminal, 0.05)
                return screen()

            deadline = time.monotonic() + 8
            marker = b"synthetic" if ordered else b"No results."
            while marker not in terminal.raw:
                terminal.read(0.02)
                assert terminal.child.poll() is None, "Automatic terminal list bypassed the pager"
                assert time.monotonic() < deadline, "Pager did not display its controls"
            navigation.drain(terminal, 0.1)
            before = bytes(terminal.raw)
            result.update(initial_bytes=len(before), initial_newlines=before.count(b"\n"),
                          exited_before_input=terminal.child.poll() is not None,
                          initial_status=terminal.child.poll(), footer_visible=True,
                          initial_seconds=time.monotonic() - terminal.started)
            (output / "initial.tty").write_bytes(before)
            assert not result["exited_before_input"], "Short or empty list bypassed the pager"
            assert result["initial_bytes"] < 40000, "Initial screen dumped excessive output"
            initial_screen, initial_lines = screen("initial-screen", 0)
            if args.expect_id_owner and items:
                assert ("ID" in initial_screen and "OWNER" in initial_screen) or \
                    ("ID:" in initial_screen and "Owned by:" in initial_screen), "ID/owner labels missing"

            terminal.send(b"i")
            navigation.drain(terminal, 0.15)
            assert screen("after-i")[0] == initial_screen, "Removed i key changed pager output"
            terminal.send(b"b")
            navigation.drain(terminal, 0.1)
            assert screen("back-at-start")[0] == initial_screen, "Back moved before the first result"

            terminal.resize(max(20, args.width - 30), args.height)
            navigation.drain(terminal, 0.3)
            screen("after-resize", 0)
            terminal.resize(args.width, args.height)
            navigation.drain(terminal, 0.3)
            assert screen("restored-size")[0] == initial_screen, "Resize lost the initial position or records"

            all_lines = list(initial_lines)
            steps = 0
            completion_started = time.monotonic()
            deadline = completion_started + max(30, len(ordered) * 0.03)
            _, rows, _ = expected_rows(args.width)
            while len(all_lines) < len(rows):
                assert time.monotonic() < deadline, "Pager traversal exceeded its completion deadline"
                change_view(b" ")
                current, visible = screen(offset=len(all_lines))
                steps += 1
                if steps == 1:
                    (output / "after-space.txt").write_text(current)
                    (output / "second-page.txt").write_text(current)
                    result["bytes_after_space"] = len(terminal.raw)
                    result["request_count_after_space"] = len(server.requests)
                    assert change_view(b"b")[0] == initial_screen, "Back did not restore the initial viewport"
                    assert change_view(b" ")[0] == current, "Forward changed cached records"
                    if expected_rows(args.width)[0]:
                        terminal.resize(max(20, args.width - 30), args.height)
                        navigation.drain(terminal, 0.3)
                        screen("resized-second-page")
                        terminal.resize(args.width, args.height)
                        navigation.drain(terminal, 0.3)
                        assert screen("restored-second-page")[0] == current, "Resize lost the model at the top"

                all_lines.extend(visible)
            assert all_lines == rows, "The pager skipped, duplicated, or reordered a model"
            result["traversal_seconds"] = time.monotonic() - completion_started
            result["forward_steps"] = steps
            result["local_pages"] = len(pages)
            bottom_screen, _ = screen("bottom")
            terminal.send(b" i")
            navigation.drain(terminal, 0.15)
            assert screen("final-stays-open")[0] == bottom_screen, "Final screen changed or exited"

            complete = "\n".join(all_lines) + "\n"
            (output / "paged-output.txt").write_text(complete)
            assert not any(hint in terminal.text() for hint in ("p: print", "p: all", "i: inline")), \
                "Obsolete controls remain"
            if args.expect_id_owner:
                assert not any(label in complete for label in ("Shutdown date:", "Created:", "Object:")), \
                    "Unrelated metadata expanded the list"
            result["verified_full_ids_and_owners"] = len(items)
            assert len(server.requests) == 1, "Models must use one HTTP request"
            assert server.requests[0]["path"] == "/v1/models?limit=100"
            terminal.send(b"q")
            navigation.finish(terminal, 0)
            restored = navigation.screen_text(terminal)
            (output / "restored-screen.txt").write_text(restored)
            assert "q: quit" not in restored and "Space/b/q" not in restored, "Quit retained pager controls"
            assert not id_pattern.search(re.sub(r"\s+", "", restored)), "Quit printed viewer records into terminal history"
            result["screen_assertions"] = "Space/b/q, inert i, resize, complete IDs, final page stays open, terminal restored"
            result["status"] = "passed"
        except Exception as error:
            result.update(status="failed", error=str(error))
            raise
        finally:
            try:
                result.update(exit_status=terminal.child.poll(), requests=server.requests, total_bytes=len(terminal.raw))
                (output / "capture.tty").write_bytes(terminal.raw)
                header = {"version": 2, "width": args.width, "height": args.height,
                          "title": "Models list pager; synthetic API"}
                (output / "capture.cast").write_text("\n".join(json.dumps(item)
                                                    for item in [header, *terminal.events]) + "\n")
                (output / "result.json").write_text(json.dumps(result, indent=2) + "\n")
            finally:
                harness.cleanup_fixture_resources(terminal.close, server.shutdown, server.server_close,
                                                  lambda: thread.join(timeout=2))
        print(json.dumps(result, indent=2))


if __name__ == "__main__":
    main()
