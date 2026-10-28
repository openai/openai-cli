#!/usr/bin/env python3
"""Drive the public image picker with synthetic input and no generation.

record.sh captures this process through the shared asciinema lifecycle.
The existing picker harness owns PTY setup, query replies, and restoration checks.
"""

import argparse
import importlib.util
import json
import pathlib
import sys
import tempfile
import threading
import time


HARNESS_PATH = pathlib.Path(__file__).resolve().parents[1] / "image_picker_harness.py"
SPEC = importlib.util.spec_from_file_location("picker_harness", HARNESS_PATH)
picker = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(picker)


class RequestTrap(picker.http.server.BaseHTTPRequestHandler):
    """Fail locally if an input unexpectedly starts an API request."""

    def log_message(self, *_):
        pass

    def do_POST(self):
        self.server.request_count += 1
        self.rfile.read(int(self.headers.get("Content-Length", "0")))
        body = b'{"error":{"message":"Synthetic layout request trap"}}'
        self.send_response(400)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    do_GET = do_POST


class RecordingTerminal(picker.Terminal):
    """Relay actual child output into the shared recorder's PTY."""

    def read(self, duration=0.05):
        start = len(self.raw)
        super().read(duration)
        data = self.raw[start:]
        if data:
            written = sys.stdout.buffer.write(data)
            if written != len(data):
                raise OSError("short write while relaying the picker recording")
            sys.stdout.buffer.flush()


def hold(terminal, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.05, deadline - time.monotonic()))
        if terminal.child.poll() is not None:
            raise AssertionError("picker exited before cancellation")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("report")
    parser.add_argument("--width", type=int, choices=(40, 80), required=True)
    parser.add_argument("--no-color", action="store_true")
    args = parser.parse_args()
    report = pathlib.Path(args.report)
    binary = str(pathlib.Path(args.binary).resolve())
    server = picker.http.server.ThreadingHTTPServer(("127.0.0.1", 0), RequestTrap)
    server.request_count = 0
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        with tempfile.TemporaryDirectory(prefix="image-picker-layout-") as home:
            environment = {
                "HOME": home,
                "PATH": "/usr/bin:/bin",
                "TERM": "xterm-256color",
                "LANG": "en_US.UTF-8",
                "OPENAI_API_KEY": "synthetic-layout-key",
                "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1",
                "OPENAI_PICKER_SHELL": "bash",
                "CI": "true",
            }
            if args.no_color:
                environment["NO_COLOR"] = "1"
            terminal = RecordingTerminal(
                binary, ["images", "generate"], environment,
                width=args.width, height=24,
            )
            try:
                picker.ready(terminal)
                terminal.send(b"\x1b[200~A tiny orange robot\x1b[201~")
                terminal.wait("A tiny orange robot")
                hold(terminal, 2)
                terminal.send(b"\t")  # Prompt to options.
                hold(terminal, 0.8)
                terminal.send(picker.DOWN)  # Size uses the existing second row.
                hold(terminal, 0.4)
                terminal.send(b"\r")  # Open choices; never generate here.
                terminal.wait("Choose size")
                hold(terminal, 1)
                terminal.send(b"\r")  # Keep the selected size.
                hold(terminal, 0.5)
                assert server.request_count == 0, "choosing settings started generation"
                terminal.send(b"\x1b")  # Esc returns to the prompt.
                hold(terminal, 0.4)
                terminal.send(b"\t\t")  # Prompt to options to command.
                hold(terminal, 1)
                terminal.send(b"\x1b[Z")  # Shift+Tab returns to options.
                hold(terminal, 0.4)
                terminal.send(b"\x1b")
                hold(terminal, 0.4)
                terminal.send(b"\x1b[200~" + " · café 🟠".encode() + b"\x1b[201~")
                terminal.wait("café")
                hold(terminal, 1.6)
                terminal.send(b"\x03")
                terminal.finish(130)
                assert server.request_count == 0, "layout demo made an API request"
                assert not list(pathlib.Path(home).rglob("image-picker.json")), "cancel saved preferences"
                if args.no_color:
                    assert b";2;" not in terminal.raw and b";5;" not in terminal.raw, "NO_COLOR emitted color"
                result = {
                    "binary": binary,
                    "width": args.width,
                    "height": 24,
                    "no_color": args.no_color,
                    "input": "A tiny orange robot",
                    "unicode_edit": " · café 🟠",
                    "exit_status": terminal.child.returncode,
                    "api_requests": server.request_count,
                    "terminal_restored": True,
                    "preferences_written": False,
                    "actions": ["paste prompt", "Tab", "Down", "Enter choice menu", "Enter current choice", "Esc", "Tab", "Tab", "Shift+Tab", "Esc", "paste Unicode", "Ctrl+C"],
                }
                report.write_text(json.dumps(result, indent=2) + "\n")
            finally:
                terminal.close()
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
        if worker.is_alive():
            raise RuntimeError("layout request trap did not stop")
    return 130


if __name__ == "__main__":
    sys.exit(main())
