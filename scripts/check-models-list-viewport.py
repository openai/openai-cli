#!/usr/bin/env python3
"""Probe large model lists without live credentials or private model records.

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
            pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("output")
    parser.add_argument("--expect-viewer", action="store_true")
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
    items = [{"id": f"synthetic-finetune-model-for-viewport-regression-with-long-readable-id-{i:05d}",
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
               "OPENAI_API_KEY": "synthetic-models-viewport-key",
               "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1"}
        result["version"] = subprocess.run([str(binary), "--version"], env=env,
                                           capture_output=True, text=True, check=True).stdout.strip()
        terminal = harness.Terminal(str(binary), ["models", "list"], env,
                                    width=args.width, height=args.height)
        try:
            def screen(name):
                value = navigation.screen_text(terminal)
                (output / (name + ".txt")).write_text(value)
                assert len(server.requests) == 1, "Browsing fetched another API page"
                return value

            def visible_ids(value):
                # Labeled IDs can wrap at narrow widths; these fixture IDs contain no whitespace.
                compact = re.sub(r"\s+", "", value)
                return re.findall(r"(?:readable-id-|synthetic-model-)(\d{5})", compact)

            def assert_id_owner(value, require_last=False):
                compact = re.sub(r"\s+", "", value)
                numbers = visible_ids(value)
                assert numbers, "No complete model IDs are visible"
                assert not any(label in value for label in
                               ("Shutdown date:", "Created:", "Object:")), "Unrelated metadata expanded the list"
                checked = [f"{args.count - 1:05d}"] if require_last else numbers[:-1] or numbers[:1]
                for number in checked:
                    item = items[int(number)]
                    identity = re.sub(r"\s+", "", item["id"])
                    owner = re.sub(r"\s+", "", item["owned_by"])
                    assert identity + owner in compact or identity + "Ownedby:" + owner in compact, \
                        "A complete ID/owner pair was shortened or mismatched"

            deadline = time.monotonic() + 8
            while time.monotonic() < deadline:
                terminal.read(0.02)
                if terminal.child.poll() is not None:
                    for _ in range(5):
                        terminal.read(0.01)
                    break
                if items and b"p:" in terminal.raw and b"q: quit" in terminal.raw:
                    break
            navigation.drain(terminal, 0.1)
            before = bytes(terminal.raw)
            result.update(initial_bytes=len(before), initial_newlines=before.count(b"\n"),
                          exited_before_input=terminal.child.poll() is not None,
                          initial_status=terminal.child.poll(),
                          footer_visible=b"q: quit" in before,
                          initial_seconds=time.monotonic() - terminal.started)
            (output / "initial.tty").write_bytes(before)
            if terminal.child.poll() is None:
                initial_screen = screen("initial-screen")
                first_ids = visible_ids(initial_screen)
                assert first_ids and first_ids[0] == "00000", ("First model missing", initial_screen)
                if args.expect_id_owner:
                    assert ("ID" in initial_screen and "OWNER" in initial_screen) or \
                        ("ID:" in initial_screen and "Owned by:" in initial_screen), "ID/owner labels missing"
                    assert_id_owner(initial_screen)
                assert "End of results" not in initial_screen, "End displayed above unread rows"
                assert "Space: more" in initial_screen and "b: back" in initial_screen
                assert len(initial_screen.splitlines()) <= args.height, "Initial screen overflowed the terminal"
                terminal.send(b" ")
                navigation.drain(terminal, 0.15)
                next_ids = visible_ids(screen("after-space"))
                assert next_ids and int(next_ids[0]) > int(first_ids[0]), "Space did not advance visible models"
                assert "00000" not in next_ids, "Space retained stale first rows"
                result["bytes_after_space"] = len(terminal.raw)
                result["request_count_after_space"] = len(server.requests)
                terminal.send(b"b")
                deadline = time.monotonic() + 8
                while "00000" not in visible_ids(navigation.screen_text(terminal)):
                    if terminal.child.poll() is not None or time.monotonic() >= deadline:
                        raise AssertionError("Back did not restore the first model")
                    terminal.read(0.03)
                assert visible_ids(screen("after-back")) == first_ids, "Back did not restore the initial model range"
                result["bytes_after_back"] = len(terminal.raw)
                terminal.resize(max(40, args.width - 30), args.height)
                navigation.drain(terminal, 0.3)
                resized_screen = screen("after-resize")
                assert "00000" in visible_ids(resized_screen), "Resize lost loaded models"
                assert "Space: more" in resized_screen and "q: quit" in resized_screen
                if args.expect_id_owner:
                    assert_id_owner(resized_screen)
                # Repeated user page-down keys visit the final loaded response.
                # This deliberately exceeds the screens needed by both fixtures.
                for _ in range((args.count + 127) // 128 + 1):
                    terminal.send(b" " * 128)
                    navigation.drain(terminal, 0.06)
                    if "End of results" in navigation.screen_text(terminal):
                        break
                navigation.wait_screen(terminal, "End of results")
                bottom_screen = screen("bottom")
                assert f"{args.count-1:05d}" in visible_ids(bottom_screen), "Last model is inaccessible"
                if args.expect_id_owner:
                    noun = "model" if args.count == 1 else "models"
                    assert f"Listed {args.count} {noun}." in bottom_screen, "Model count missing"
                    assert_id_owner(bottom_screen, require_last=True)
                terminal.send(b"b")
                navigation.drain(terminal, 0.15)
                assert "End of results" not in screen("back-from-bottom"), "End stayed visible above the bottom"
                quit_started = time.monotonic()
                terminal.send(b"q")
                navigation.finish(terminal, 0)
                result["quit_seconds"] = time.monotonic() - quit_started
                result["screen_assertions"] = "loaded, forward, back, resize, last model, end footer, restored terminal"
            else:
                navigation.finish(terminal, 0)
            result["exit_status"] = terminal.child.poll()
            result["requests"] = server.requests
            result["total_bytes"] = len(terminal.raw)
            (output / "capture.tty").write_bytes(terminal.raw)
            header = {"version": 2, "width": args.width, "height": args.height,
                      "title": "Models list viewport; synthetic API"}
            (output / "capture.cast").write_text("\n".join(json.dumps(item)
                                                for item in [header, *terminal.events]) + "\n")
            (output / "result.json").write_text(json.dumps(result, indent=2) + "\n")
            print(json.dumps(result, indent=2))
            assert len(server.requests) == 1, "Models must use one HTTP request"
            assert server.requests[0]["path"] == "/v1/models"
            if args.expect_viewer:
                assert not result["exited_before_input"], "Large list bypassed interactive browsing"
                assert result["footer_visible"], "Viewer lacks a visible quit control"
                assert result["initial_bytes"] < 40000, "Initial view dumped excessive output"
                assert result["request_count_after_space"] == 1, "Local browsing fetched another API page"
            assert result["exit_status"] == 0, "Command failed or did not exit"
        finally:
            terminal.close()
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)


if __name__ == "__main__":
    main()
