#!/usr/bin/env python3
"""Relay real CLI bytes and demonstrate local model-list navigation."""

import hashlib
import importlib.util
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import termios
import time


spec = importlib.util.spec_from_file_location(
    "models_demo_terminal", pathlib.Path(__file__).resolve().parents[2] / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


class RelayTerminal(harness.Terminal):
    def read(self, duration=0.05):
        offset = len(self.raw)
        super().read(duration)
        chunk = self.raw[offset:]
        if sys.stdout.buffer.write(chunk) != len(chunk):
            raise OSError("short terminal relay write")
        sys.stdout.buffer.flush()

    def send(self, value):
        if os.write(self.master, value) != len(value):
            raise OSError("short navigation key write")


def drain(terminal, seconds):
    deadline = time.monotonic() + seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline - time.monotonic())))


def screen_text(terminal):
    with tempfile.TemporaryDirectory(prefix="models-demo-screen-") as temporary:
        recording = pathlib.Path(temporary) / "screen.cast"
        transcript = pathlib.Path(temporary) / "screen.txt"
        header = {"version": 2, "width": 110, "height": 27}
        recording.write_text("\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
        subprocess.run([os.environ["DEMO_ASCIINEMA"], "convert", "-f", "txt", str(recording), str(transcript)],
                       capture_output=True, text=True, check=True, timeout=10,
                       env={"PATH": "/usr/bin:/bin", "HOME": temporary,
                            "ASCIINEMA_STATE_HOME": temporary + "/state",
                            "ASCIINEMA_CONFIG_HOME": temporary + "/config"})
        return transcript.read_text()


def visible_ids(screen):
    return re.findall(r"synthetic-finetune-model-for-viewport-regression-with-long-readable-id-\d{5}", screen)


def assert_names(screen, first):
    ids = visible_ids(screen)
    expected = [f"synthetic-finetune-model-for-viewport-regression-with-long-readable-id-{i:05d}"
                for i in range(first, first + len(ids))]
    if len(ids) < 20 or ids != expected:
        raise AssertionError("candidate does not show exact ascending model IDs")
    for metadata in ("ID:", "Owned by:", "Shutdown date:", "Created:", "synthetic-owner", "2030-01-01"):
        if metadata in screen:
            raise AssertionError("candidate includes default metadata")
    if any(line.strip() not in ids for line in screen.splitlines() if "synthetic-finetune-" in line):
        raise AssertionError("candidate model row contains additional data")
    return ids


def assert_baseline(terminal):
    text = terminal.text()
    expected = [f"synthetic-finetune-model-for-viewport-regression-with-long-readable-id-{i:05d}"
                for i in range(119, -1, -1)]
    if visible_ids(text) != expected:
        raise AssertionError("baseline did not print all 120 exact IDs in response order")
    if text.count("Owned by: synthetic-owner") != 120 or text.count("Shutdown date: 2030-01-01") != 60:
        raise AssertionError("baseline did not print the expected model metadata")
    if "p: print" in text or "Space: more" in text:
        raise AssertionError("baseline unexpectedly entered a model viewer")
    if terminal.raw.count(b"\n") <= 27:
        raise AssertionError("baseline fixture did not overflow the terminal")
    return expected


def send_key(terminal, evidence, label, value):
    if terminal.child.poll() is not None:
        raise AssertionError("viewer exited before navigation input")
    action = {"key": label, "seconds": time.monotonic() - terminal.started,
              "bytes_before": len(terminal.raw)}
    evidence["keys"].append(action)
    terminal.send(value)
    return action


def check_action(terminal, action, running=True):
    action["bytes_after"] = len(terminal.raw)
    if action["bytes_after"] <= action["bytes_before"] or action["bytes_after"] >= 40000:
        raise AssertionError("navigation output is missing or unbounded")
    if running and terminal.child.poll() is not None:
        raise AssertionError("viewer exited during navigation")


def main():
    mode = os.environ["DEMO_SCENE_NAME"]
    output = pathlib.Path(os.environ["DEMO_EVIDENCE_BASE"])
    if mode not in {"before", "after"} or not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("scene requires before/after mode and terminal input/output")
    binary = shutil.which("openai")
    if binary is None:
        raise RuntimeError("selected CLI binary is missing")
    evidence = {"scene": mode, "command": ["openai", "models", "list"], "keys": [],
                "binary_sha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),
                "width": 110, "height": 27, "models": 120}
    print("$ openai models list", flush=True)
    # The child PTY already applies terminal newline conversion. Relay its exact bytes.
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    try:
        termios.tcsetattr(1, termios.TCSANOW, relay)
        with tempfile.TemporaryDirectory(prefix="models-demo-home-") as temporary:
            env = {"PATH": "/usr/bin:/bin", "HOME": temporary, "TERM": "xterm-256color",
                   "LANG": "en_US.UTF-8", "NO_COLOR": "1", "FORCE_COLOR": "0", "GOMAXPROCS": "2",
                   "PAGER": "cat", "OPENAI_API_KEY": "synthetic-demo-key",
                   "OPENAI_BASE_URL": os.environ["OPENAI_BASE_URL"]}
            terminal = None
            try:
                terminal = RelayTerminal(binary, ["models", "list"], env, width=110, height=27)
                run_scene(terminal, mode, output, evidence)
            finally:
                if terminal is not None:
                    terminal.close()
    finally:
        # Restore output processing even when child creation or cleanup fails.
        termios.tcsetattr(1, termios.TCSANOW, original)
    output.with_suffix(".json").write_text(json.dumps(evidence, indent=2) + "\n")
    print("\n$ ", end="", flush=True)
    time.sleep(2)


def run_scene(terminal, mode, output, evidence):
    if mode == "before":
        terminal.finish(0, timeout=20, expect_picker=False)
        evidence["returned_ids"] = assert_baseline(terminal)
        evidence["initial_seconds"] = time.monotonic() - terminal.started
        evidence["initial_bytes"] = len(terminal.raw)
        evidence["initial_newlines"] = terminal.raw.count(b"\n")
        output.with_suffix(".initial.txt").write_text(screen_text(terminal))
        save_capture(terminal, output, evidence)
        return
    deadline = time.monotonic() + 20
    while terminal.child.poll() is None:
        terminal.read()
        if b"p: print all 120 records, quit" in terminal.raw and b"Space: more" in terminal.raw:
            break
        if time.monotonic() >= deadline:
            raise TimeoutError("scene did not reach its initial result")
    drain(terminal, 0.25)
    evidence["initial_seconds"] = time.monotonic() - terminal.started
    evidence["initial_bytes"] = len(terminal.raw)
    evidence["initial_newlines"] = terminal.raw.count(b"\n")
    if terminal.child.poll() is not None or len(terminal.raw) >= 40000:
        raise AssertionError("large result did not stay inside the viewer")
    initial = screen_text(terminal)
    output.with_suffix(".initial.txt").write_text(initial)
    evidence["initial_ids"] = assert_names(initial, 0)
    if "ID" not in [line.strip() for line in initial.splitlines()] or "p: print all 120 records, quit" not in initial:
        raise AssertionError("candidate omits the ID heading or complete print-all hint")
    drain(terminal, 1.5)
    action = send_key(terminal, evidence, "Space", b" ")
    drain(terminal, 1.0)
    check_action(terminal, action)
    forward = screen_text(terminal)
    output.with_suffix(".forward.txt").write_text(forward)
    evidence["forward_ids"] = assert_names(forward, len(evidence["initial_ids"]))
    if "p: print all 120 records, quit" not in forward:
        raise AssertionError("candidate changed the print-all hint during navigation")
    drain(terminal, 0.75)
    action = send_key(terminal, evidence, "b", b"b")
    drain(terminal, 1.0)
    check_action(terminal, action)
    back = screen_text(terminal)
    output.with_suffix(".back.txt").write_text(back)
    if assert_names(back, 0) != evidence["initial_ids"]:
        raise AssertionError("b did not restore the exact initial model IDs")
    drain(terminal, 0.75)
    action = send_key(terminal, evidence, "q", b"q")
    terminal.finish(0, timeout=5, expect_picker=True)
    check_action(terminal, action, running=False)
    save_capture(terminal, output, evidence)


def save_capture(terminal, output, evidence):
    evidence["exit_status"] = terminal.child.returncode
    evidence["relayed_bytes"] = len(terminal.raw)
    evidence["relayed_sha256"] = hashlib.sha256(terminal.raw).hexdigest()
    output.with_suffix(".tty").write_bytes(terminal.raw)
    header = {"version": 2, "width": 110, "height": 27,
              "title": "Models list; 120 synthetic records"}
    output.with_suffix(".child.cast").write_text(
        "\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")


if __name__ == "__main__":
    main()
