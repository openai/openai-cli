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
        header = {"version": 2, "width": terminal.initial_size[0], "height": terminal.initial_size[1]}
        recording.write_text("\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")
        subprocess.run([os.environ["DEMO_ASCIINEMA"], "convert", "-f", "txt", str(recording), str(transcript)],
                       capture_output=True, text=True, check=True, timeout=10,
                       env={"PATH": "/usr/bin:/bin", "HOME": temporary,
                            "ASCIINEMA_STATE_HOME": temporary + "/state",
                            "ASCIINEMA_CONFIG_HOME": temporary + "/config"})
        return transcript.read_text()


def visible_ids(screen):
    return re.findall(r"demo-text-2026-10-01-\d{3}", screen)


def assert_rows(screen, records, first, mode, width, heading=False):
    ids = visible_ids(screen)
    expected = [item["id"] for item in records[first:first + len(ids)]]
    if len(ids) < (8 if mode != "before" and width == 40 else 20) or ids != expected:
        raise AssertionError("viewer does not show complete ascending model IDs")
    for metadata in ("Shutdown date:", "Created:", "Object:", "2030-01-01"):
        if metadata in screen:
            raise AssertionError("unrelated metadata expanded the model view")
    lines = [line.rstrip() for line in screen.splitlines()]
    if mode == "before":
        if any(item["owned_by"] in screen for item in records) or "OWNER" in screen or "Owned by:" in screen:
            raise AssertionError("baseline unexpectedly includes owners")
        if any(line not in ids for line in lines if "demo-text-" in line):
            raise AssertionError("baseline ID row contains additional fields")
        if heading and "ID" not in lines:
            raise AssertionError("baseline ID heading missing")
    elif width == 110:
        if heading and not any(line.split() == ["ID", "OWNER"] for line in lines):
            raise AssertionError("ID and OWNER column headings missing")
        for item in records[first:first + len(ids)]:
            if not any(line.split() == [item["id"], item["owned_by"]] for line in lines):
                raise AssertionError("table changed or mismatched an ID and owner")
    else:
        if any(line.split() == ["ID", "OWNER"] for line in lines):
            raise AssertionError("narrow view did not use complete labeled fields")
        for item in records[first:first + len(ids)]:
            index = lines.index("ID: " + item["id"])
            if index + 1 >= len(lines) or lines[index + 1] != "Owned by: " + item["owned_by"]:
                raise AssertionError("narrow view shortened or mismatched an ID and owner")
    if any(len(line) > width for line in lines):
        raise AssertionError("viewer output overflowed its width")
    return ids


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
    width = int(os.environ["DEMO_WIDTH"])
    if mode not in {"before", "after", "loading"} or width not in {40, 110} or not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise RuntimeError("scene requires a supported mode, width, and terminal input/output")
    records = sorted(json.loads(pathlib.Path(os.environ["DEMO_FIXTURE_METADATA"]).read_text())["records"],
                     key=lambda item: item["id"])
    binary = shutil.which("openai")
    if binary is None:
        raise RuntimeError("selected CLI binary is missing")
    evidence = {"scene": mode, "command": ["openai", "models", "list"], "keys": [],
                "binary_sha256": hashlib.sha256(pathlib.Path(binary).read_bytes()).hexdigest(),
                "width": width, "height": 26, "models": len(records)}
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
                terminal = RelayTerminal(binary, ["models", "list"], env, width=width, height=26)
                run_scene(terminal, mode, output, evidence, records)
            finally:
                if terminal is not None:
                    terminal.close()
    finally:
        # Restore output processing even when child creation or cleanup fails.
        termios.tcsetattr(1, termios.TCSANOW, original)
    output.with_suffix(".json").write_text(json.dumps(evidence, indent=2) + "\n")
    print("\n$ ", end="", flush=True)
    time.sleep(2)


def run_scene(terminal, mode, output, evidence, records):
    if mode == "loading":
        terminal.wait("Loading models", timeout=8)
        gate = pathlib.Path(os.environ["DEMO_LOADING_GATE"])
        if not (gate / "requested").exists() or visible_ids(terminal.text()):
            raise AssertionError("loading feedback did not precede the held response")
        evidence["loading_seconds"] = time.monotonic() - terminal.started
        output.with_suffix(".loading.txt").write_text(screen_text(terminal))
        drain(terminal, 1.2)
        evidence["release_seconds"] = time.monotonic() - terminal.started
        (gate / "release").write_text("show response\n")
    deadline = time.monotonic() + 20
    while terminal.child.poll() is None:
        terminal.read()
        if b"p: print all 48 records, quit" in terminal.raw and b"Space: more" in terminal.raw:
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
    evidence["initial_ids"] = assert_rows(initial, records, 0, mode, evidence["width"], heading=True)
    if "p: print all 48 records, quit" not in initial or "Loading models" in initial:
        raise AssertionError("loaded view has stale loading feedback or lacks its print-all hint")
    first_result = terminal.raw.find(records[0]["id"].encode())
    if terminal.raw.rfind(b"Loading models") > first_result:
        raise AssertionError("loading feedback continued after result output")
    drain(terminal, 1.5)
    if mode == "loading":
        action = send_key(terminal, evidence, "q", b"q")
        terminal.finish(0, timeout=5, expect_picker=True)
        check_action(terminal, action, running=False)
        save_capture(terminal, output, evidence)
        return
    action = send_key(terminal, evidence, "Space", b" ")
    drain(terminal, 1.0)
    check_action(terminal, action)
    forward = screen_text(terminal)
    output.with_suffix(".forward.txt").write_text(forward)
    evidence["forward_ids"] = assert_rows(forward, records, len(evidence["initial_ids"]), mode, evidence["width"])
    if "p: print all 48 records, quit" not in forward:
        raise AssertionError("candidate changed the print-all hint during navigation")
    drain(terminal, 0.75)
    action = send_key(terminal, evidence, "b", b"b")
    drain(terminal, 1.0)
    check_action(terminal, action)
    back = screen_text(terminal)
    output.with_suffix(".back.txt").write_text(back)
    if assert_rows(back, records, 0, mode, evidence["width"], heading=True) != evidence["initial_ids"]:
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
    header = {"version": 2, "width": evidence["width"], "height": evidence["height"],
              "title": "Models list; 48 synthetic records"}
    output.with_suffix(".child.cast").write_text(
        "\n".join(json.dumps(item) for item in [header, *terminal.events]) + "\n")


if __name__ == "__main__":
    main()
