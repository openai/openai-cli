#!/usr/bin/env python3
"""Relay real CLI PTYs while exercising manual and guided Batch navigation."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import sys
import termios
import time

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("batch_demo_terminal", Path(__file__).resolve().parents[2] / "image_picker_harness.py")
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


def pause(terminal, seconds=1.1):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        terminal.read(min(0.03, max(0, until-time.monotonic())))


def current_screen(terminal, alternate):
    # Decode the current screen independently. The shared inline decoder does
    # not keep separate primary and alternate buffers across menu transitions.
    switch = b"\x1b[?1049h" if alternate else b"\x1b[?1049l"
    start = terminal.raw.rfind(switch)
    assert start >= 0, "missing alternate-screen transition"
    return harness.screen(terminal.raw[start:], width=104, height=42).decode()


def main():
    output = Path(os.environ["DEMO_EVIDENCE"])
    mode = os.environ["DEMO_SCENE"]
    environment = {name: os.environ[name] for name in ("PATH", "HOME", "OPENAI_BASE_URL")}
    environment.update(TERM="xterm-256color", COLORTERM="truecolor", COLORFGBG="15;0", LANG="en_US.UTF-8", GOMAXPROCS="2",
                       OPENAI_API_KEY="synthetic-demo-key", OPENAI_CLI_NO_UPDATE_CHECK="1",
                       XDG_CONFIG_HOME=environment["HOME"], XDG_CACHE_HOME=environment["HOME"])
    if os.environ.get("NO_COLOR"):
        environment["NO_COLOR"] = os.environ["NO_COLOR"]
    evidence = {"synthetic": True, "mode": mode, "terminal": "macOS PTY, 104x42", "commands": [], "checkpoints": {}, "screens": {},
                "binary_sha256": hashlib.sha256(Path(shutil.which("openai")).read_bytes()).hexdigest()}
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    terminal = None
    try:
        print(("Before: copy a batch ID into each command." if mode == "before" else
               "After: choose a batch, find a request, then open its details."), flush=True)
        print("Synthetic local API. No personal API key or model charges.\n", flush=True)
        termios.tcsetattr(1, termios.TCSANOW, relay)
        if mode == "before":
            commands = [["batches", "list", "--max-items", "2"],
                        ["batches", "results", "batch_partial_demo"],
                        ["batches", "results", "batch_partial_demo", "--file", "error"]]
            for args in commands:
                print("\r\n$ openai " + " ".join(args), end="\r\n", flush=True)
                terminal = RelayTerminal("openai", args, environment, width=104, height=42,
                                         max_output_bytes=1_000_000, deadline=time.monotonic()+15)
                terminal.finish(0, expect_picker=False)
                pause(terminal)
                evidence["commands"].append({"arguments": args, "exit": terminal.child.returncode})
                terminal.close()
                terminal = None
        else:
            print("$ openai batches browse\r", flush=True)
            terminal = RelayTerminal("openai", ["batches", "browse"], environment, width=104, height=42,
                                     max_output_bytes=1_000_000, deadline=time.monotonic()+45)
            terminal.wait("Search  ")
            pause(terminal)
            terminal.send(b"\x1b[B")
            pause(terminal, 0.4)
            terminal.send(b"\r")
            terminal.wait("View results")
            pause(terminal)
            evidence["checkpoints"]["menu"] = time.monotonic()-terminal.started
            evidence["screens"]["menu"] = current_screen(terminal, alternate=True)
            menu_lines = evidence["screens"]["menu"].splitlines()
            assert any("batch_partial_demo" in line for line in menu_lines), "missing batch title"
            columns = []
            for label in ("View results", "View errors", "Batch details"):
                matches = [line for line in menu_lines if line.strip(" │>─") == label]
                assert len(matches) == 1, "menu label missing or duplicated: " + label
                columns.append(matches[0].index(label))
            assert len(set(columns)) == 1, "menu labels are misaligned"
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("Ctrl+V", after=offset)
            pause(terminal, 0.4)
            terminal.send(b"\x1b[200~sentiment-positive\x1b[201~")
            terminal.wait("1 match", after=offset)
            pause(terminal)
            evidence["checkpoints"]["result_requests"] = time.monotonic()-terminal.started
            evidence["screens"]["result_requests"] = current_screen(terminal, alternate=True)
            assert "sentiment-positive" in evidence["screens"]["result_requests"], "matching result request missing"
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("PgUp/PgDn page", after=offset)
            pause(terminal, 1.6)
            evidence["checkpoints"]["results"] = time.monotonic()-terminal.started
            evidence["screens"]["results"] = current_screen(terminal, alternate=True)
            result_lines = evidence["screens"]["results"].splitlines()
            assert any(line.strip(" │") == "batch_partial_demo / Record 2" for line in result_lines), "result context missing from panel"
            result_text = [line.strip(" │") for line in result_lines]
            assert "sentiment-positive  Completed" in result_text, "selected result identity and outcome missing"
            assert "Output" in result_text and "positive" in result_text, "grouped result output missing"
            assert result_text.index("Output") < result_text.index("positive"), "result output precedes its heading"
            assert "Tokens  31 input  5 output  36 total" in result_text, "compact token summary missing"
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("Ctrl+V", after=offset)
            pause(terminal, 0.4)
            returned = current_screen(terminal, alternate=True)
            assert "sentiment-positive" in returned and "1 match" in returned, "result search was not retained"
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("View results", after=offset)
            terminal.send(b"\x1b[B")
            pause(terminal, 0.4)
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("Ctrl+V", after=offset)
            pause(terminal)
            evidence["checkpoints"]["error_requests"] = time.monotonic()-terminal.started
            evidence["screens"]["error_requests"] = current_screen(terminal, alternate=True)
            assert "sentiment-negative" in evidence["screens"]["error_requests"], "error request missing"
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("Synthetic service error", after=offset)
            terminal.wait("PgUp/PgDn page", after=offset)
            pause(terminal, 1.8)
            evidence["checkpoints"]["errors"] = time.monotonic()-terminal.started
            evidence["screens"]["errors"] = current_screen(terminal, alternate=True)
            error_lines = evidence["screens"]["errors"].splitlines()
            assert any(line.strip(" │") == "Request error" for line in error_lines), "error detail heading missing"
            assert any(line.strip(" │") == "batch_partial_demo / Record 1" for line in error_lines), "error context missing from panel"
            assert any(line.strip(" │") == "sentiment-negative  Request failed" for line in error_lines), "selected error identity and outcome missing"
            assert any(line.strip(" │") == "Error details" for line in error_lines), "grouped error section missing"
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("Ctrl+V", after=offset)
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("View results", after=offset)
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("Search  ", after=offset)
            pause(terminal, 0.4)
            terminal.send(b"\x1b")
            terminal.finish(0, expect_alternate_screen=True)
            evidence["commands"].append({"arguments": ["batches", "browse"], "exit": terminal.child.returncode})
            header = {"version": 2, "width": 104, "height": 42, "title": "Batch browser; actual CLI, synthetic data"}
            output.with_suffix(".child.cast").write_text("\n".join(json.dumps(item) for item in [header, *terminal.events])+"\n")
            output.with_suffix(".tty").write_bytes(terminal.raw)
    finally:
        try:
            if terminal is not None:
                terminal.close()
        finally:
            termios.tcsetattr(1, termios.TCSANOW, original)
    evidence["cleanup"] = "all owned CLI children joined; PTY mode restored by harness"
    output.with_suffix(".json").write_text(json.dumps(evidence, indent=2)+"\n")
    print("\r\nTerminal replay | synthetic data\r\n$ ", end="", flush=True)
    time.sleep(1)


if __name__ == "__main__":
    main()
