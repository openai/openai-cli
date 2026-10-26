#!/usr/bin/env python3
"""Probe Models pager navigation without live credentials or private model records.

Usage: python3 -I -B scripts/check-models-list-viewport.py BINARY OUTPUT
The default fixture contains 11,892 synthetic records in one HTTP response.
"""
import argparse
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
import textwrap
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
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    server.body = json.dumps({"object": "list", "data": items}, separators=(",", ":")).encode()
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
            def pager_footer(width, complete=False):
                value = "End   b: back   q: quit" if complete else "Space: more   b: back   q: quit"
                if len(value) > width:
                    value = value.replace("   ", " ")
                if len(value) > width:
                    value = "Space/b/q"
                return value[:width]

            def expected_rows(width):
                if not items:
                    return ["No results."]
                id_width = max(len("ID"), *(len(item["id"]) for item in items))
                owner_width = max(len("OWNER"), *(len(item["owned_by"]) for item in items))
                if id_width + 2 + owner_width <= width:
                    logical = ["ID".ljust(id_width) + "  OWNER"]
                    logical += [item["id"].ljust(id_width) + "  " + item["owned_by"] for item in items]
                    logical += [""]
                else:
                    logical = []
                    for item in items:
                        logical += ["ID: " + item["id"], "Owned by: " + item["owned_by"], ""]
                noun = "model" if len(items) == 1 else "models"
                logical += [f"Listed {len(items)} {noun}."]
                logical += textwrap.wrap("Details: --format json", width, break_on_hyphens=False)
                return [part.rstrip() for line in logical
                        for part in ([line[i:i + width] for i in range(0, len(line), width)] or [""])]

            def screen(name=None, offset=None):
                assert terminal.child.poll() is None, "Pager exited before q"
                value = navigation.screen_text(terminal)
                if name is not None:
                    (output / (name + ".txt")).write_text(value)
                assert len(server.requests) == 1, "Paging fetched another API response"
                width, height = terminal.size()
                lines = [line.rstrip() for line in value.rstrip("\n").splitlines()]
                assert lines, "Pager screen is empty"
                assert lines[-1] in {pager_footer(width), pager_footer(width, True)}, \
                    "Pager lacks the Space/b/q or final footer"
                assert len(lines) <= height, "Pager screen overflowed the terminal"
                data = lines[:-1]
                if offset is not None:
                    rows = expected_rows(width)
                    final = offset == max(0, len(rows) - height + 1)
                    expected = rows[offset:offset + height - 1]
                    assert data == expected, f"Pager rows differ at physical offset {offset}: {data!r} != {expected!r}"
                    assert lines[-1] == pager_footer(width, final), "Pager final-page state differs from its content"
                return value, data

            id_pattern = re.compile(
                r"(?:synthetic-finetune-model-for-viewport-regression-with-long-readable-id-|"
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
            footers = [pager_footer(args.width).encode(),
                       pager_footer(args.width, True).encode()]
            while not any(footer and footer in terminal.raw for footer in footers):
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

            expected = expected_rows(args.width)
            viewport_height = args.height - 1
            last_offset = max(0, len(expected) - viewport_height)
            offset = 0
            all_lines = list(initial_lines[:len(expected)])
            steps = 0
            completion_started = time.monotonic()
            deadline = completion_started + max(20, args.count * 0.02)
            while offset < last_offset:
                assert time.monotonic() < deadline, "Pager traversal exceeded its completion deadline"
                next_screen, lines = change_view(b" ")
                offset = min(offset + viewport_height, last_offset)
                screen(offset=offset)
                steps += 1
                # Validate the exact clamped viewport before removing its known overlap.
                overlap = len(all_lines) - offset
                all_lines.extend(lines[overlap:min(viewport_height, len(expected) - offset)])
                if steps == 1:
                    (output / "after-space.txt").write_text(next_screen)
                    result["bytes_after_space"] = len(terminal.raw)
                    result["request_count_after_space"] = len(server.requests)
                    assert change_view(b"b")[0] == initial_screen, "Back did not restore the first viewport"
                    assert change_view(b" ")[0] == next_screen, "Forward after Back changed the loaded results"
            result["traversal_seconds"] = time.monotonic() - completion_started
            result["forward_steps"] = steps
            bottom_screen, _ = screen("bottom", last_offset)
            terminal.send(b" i")
            navigation.drain(terminal, 0.15)
            assert screen("final-stays-open", last_offset)[0] == bottom_screen, \
                "Space or i dismissed or changed the final page"

            complete = "\n".join(all_lines) + "\n"
            (output / "paged-output.txt").write_text(complete)
            assert all_lines == expected, "Models were missing, duplicated, shortened, reordered, or mismatched"
            assert not any(hint in terminal.text() for hint in ("p: print", "p: all", "i: inline")), \
                "Obsolete controls remain"
            if args.expect_id_owner:
                assert not any(label in complete for label in ("Shutdown date:", "Created:", "Object:")), \
                    "Unrelated metadata expanded the list"
            result["verified_full_ids_and_owners"] = len(items)
            assert len(server.requests) == 1, "Models must use one HTTP request"
            assert server.requests[0]["path"] == "/v1/models"
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
