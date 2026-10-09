#!/usr/bin/env python3
"""Drive the real creation form and relay its unchanged terminal bytes."""

import hashlib
import importlib.util
import json
import os
import pathlib
import shutil
import signal
import subprocess
import sys
import termios
import tempfile
import time

spec = importlib.util.spec_from_file_location(
    "webhook_demo_terminal", pathlib.Path(__file__).resolve().parents[2] / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


class RelayTerminal(harness.Terminal):
    def read(self, duration=0.05):
        offset = len(self.raw)
        super().read(duration)
        if len(self.raw) > 500000:
            raise RuntimeError("demo output exceeded its fixture budget")
        chunk = self.raw[offset:]
        if sys.stdout.buffer.write(chunk) != len(chunk):
            raise OSError("short terminal relay write")
        sys.stdout.buffer.flush()

    def send(self, value):
        if os.write(self.master, value) != len(value):
            raise OSError("short keyboard write")


def pause(terminal, seconds=1):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline - time.monotonic())))


def checkpoint(terminal, evidence, name):
    pause(terminal)
    screen = wait_screen(terminal, "Create webhook endpoint")
    if screen.count("Create webhook endpoint") != 1:
        raise AssertionError("rendered screen retained an old form title")
    if "0 selected · 4 matching events" in screen:
        raise AssertionError("rendered screen retained the unfiltered event list")
    if name in {"review", "review-return", "confirm"} and "Search:" in screen:
        raise AssertionError("review retained the event-search screen")
    evidence["screens"][name] = screen
    evidence["checkpoints"][name] = time.monotonic() - terminal.started


def wait_screen(terminal, marker, timeout=8):
    deadline = time.monotonic() + timeout
    with tempfile.TemporaryDirectory(prefix="webhook-demo-screen-") as directory:
        recording = pathlib.Path(directory) / "screen.cast"
        transcript = pathlib.Path(directory) / "screen.txt"
        while time.monotonic() < deadline:
            terminal.read(0.05)
            header = {"version": 2, "width": terminal.initial_size[0], "height": terminal.initial_size[1]}
            recording.write_text("\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
            subprocess.run([os.environ["DEMO_ASCIINEMA"], "convert", "--overwrite", "-f", "txt", str(recording), str(transcript)],
                           capture_output=True, check=True, timeout=5,
                           env={"PATH": "/usr/bin:/bin", "HOME": directory,
                                "ASCIINEMA_CONFIG_HOME": directory + "/config", "ASCIINEMA_STATE_HOME": directory + "/state"})
            screen = transcript.read_text()
            if marker in screen:
                return screen
            if terminal.child.poll() is not None:
                break
    raise AssertionError("rendered screen omitted " + marker)


def paste(terminal, value):
    terminal.send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


def interrupted(*_):
    raise InterruptedError("demo interrupted")


def main():
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("guided demo requires terminal input and output")
    width = int(os.environ["DEMO_WIDTH"])
    output = pathlib.Path(os.environ["DEMO_EVIDENCE_BASE"])
    binary = shutil.which("openai")
    if width not in {40, 80} or binary is None:
        raise RuntimeError("unsupported demo width or missing CLI")
    for name in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(name, interrupted)
    evidence = {"width": width, "height": 24, "checkpoints": {}, "screens": {},
                "binary_sha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest()}
    print("$ openai webhooks create", flush=True)
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    terminal = None
    try:
        termios.tcsetattr(1, termios.TCSANOW, relay)
        env = {key: os.environ[key] for key in ("PATH", "HOME", "OPENAI_BASE_URL")}
        env.update({"TERM": "xterm-256color", "LANG": "en_US.UTF-8", "NO_COLOR": "1", "FORCE_COLOR": "0",
                    "GOMAXPROCS": "2", "OPENAI_API_KEY": "synthetic-demo-key", "XDG_CONFIG_HOME": env["HOME"]})
        terminal = RelayTerminal(binary, ["webhooks", "create"], env, width=width, height=24)
        terminal.wait("Name (optional")
        paste(terminal, "Response notifications")
        checkpoint(terminal, evidence, "name")
        terminal.send(b"\r")
        terminal.wait("Receiver URL")
        paste(terminal, "https://example.com/webhook")
        checkpoint(terminal, evidence, "url")
        terminal.send(b"\r")
        terminal.wait("Space select")
        paste(terminal, "response")
        wait_screen(terminal, "2 matching events")
        terminal.send(b" ")
        wait_screen(terminal, "1 selected")
        terminal.send(harness.DOWN)
        pause(terminal, 0.2)
        terminal.send(b" ")
        wait_screen(terminal, "2 selected")
        checkpoint(terminal, evidence, "events")
        terminal.send(b"\x15")  # Ctrl+U clears the search, not the selected events.
        paste(terminal, "no-matching-demo-event")
        wait_screen(terminal, "0 matching events")
        checkpoint(terminal, evidence, "no-matches")
        terminal.send(b"\x15")
        paste(terminal, "response")
        wait_screen(terminal, "2 selected · 2 matching events")
        checkpoint(terminal, evidence, "restored-events")
        terminal.send(b"\r")
        wait_screen(terminal, "[No]   Yes, create")
        checkpoint(terminal, evidence, "review")
        terminal.send(b"\x1b[Z")  # Shift+Tab returns to the selected events.
        wait_screen(terminal, "2 selected · 2 matching events")
        checkpoint(terminal, evidence, "back")
        terminal.send(b"\r")
        wait_screen(terminal, "[No]   Yes, create")
        checkpoint(terminal, evidence, "review-return")
        terminal.send(b"y")
        wait_screen(terminal, "[Yes, create]")
        checkpoint(terminal, evidence, "confirm")
        pathlib.Path(os.environ["DEMO_CONFIRMATION_FILE"]).write_text("explicit Yes selected before Enter\n")
        terminal.send(b"\r")
        terminal.finish(0, timeout=12)
        evidence["result_screen"] = wait_screen(terminal, "whsec_fake_for_demo_only")
        for stale in ("Create webhook endpoint", "Create endpoint now?"):
            if stale in evidence["result_screen"]:
                raise AssertionError("receipt retained the active form: " + stale)
        for expected in ("whsec_fake_for_demo_only", "save the signing secret", "verify signatures", "webhooks test"):
            if expected not in terminal.text():
                raise AssertionError("creation output omitted " + expected)
        evidence["exit_status"] = terminal.child.returncode
        evidence["relayed_sha256"] = hashlib.sha256(terminal.raw).hexdigest()
        output.with_suffix(".tty").write_bytes(terminal.raw)
        header = {"version": 2, "width": width, "height": 24, "title": "Guided webhook creation; synthetic data"}
        output.with_suffix(".child.cast").write_text(
            "\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
    finally:
        try:
            if terminal is not None:
                terminal.close()
        finally:
            termios.tcsetattr(1, termios.TCSANOW, original)
    output.with_suffix(".json").write_text(json.dumps(evidence, indent=2) + "\n")
    pathlib.Path(os.environ["DEMO_STATUS_FILE"]).write_text("0\n")
    # Keep the recorder label visible after the real result scrolls the initial heading.
    print("\nTerminal replay | synthetic data\n$ ", end="", flush=True)
    time.sleep(3)


if __name__ == "__main__":
    main()
