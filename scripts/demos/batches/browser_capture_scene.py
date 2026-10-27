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


def main():
    output = Path(os.environ["DEMO_EVIDENCE"])
    mode = os.environ["DEMO_SCENE"]
    environment = {name: os.environ[name] for name in ("PATH", "HOME", "OPENAI_BASE_URL")}
    environment.update(TERM="xterm-256color", LANG="en_US.UTF-8", NO_COLOR="1", GOMAXPROCS="2",
                       OPENAI_API_KEY="synthetic-demo-key", OPENAI_CLI_NO_UPDATE_CHECK="1",
                       XDG_CONFIG_HOME=environment["HOME"], XDG_CACHE_HOME=environment["HOME"])
    evidence = {"synthetic": True, "mode": mode, "terminal": "macOS PTY, 104x42", "commands": [], "checkpoints": {}, "screens": {},
                "binary_sha256": hashlib.sha256(Path(shutil.which("openai")).read_bytes()).hexdigest()}
    original = termios.tcgetattr(1)
    relay = termios.tcgetattr(1)
    relay[1] &= ~termios.OPOST
    terminal = None
    try:
        print(("Before: copy a batch ID into each command." if mode == "before" else
               "After: choose a batch, then Results or Errors."), flush=True)
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
            terminal.wait("Search covers this page only")
            pause(terminal)
            terminal.send(b"\x1b[B")
            pause(terminal, 0.4)
            terminal.send(b"\r")
            terminal.wait("Batch: batch_partial_demo")
            pause(terminal)
            evidence["checkpoints"]["menu"] = time.monotonic()-terminal.started
            evidence["screens"]["menu"] = harness.screen(terminal.raw, width=104, height=42).decode()
            for line in ("Batch: batch_partial_demo", "> Status", "  Results", "  Errors"):
                assert line in evidence["screens"]["menu"].splitlines(), "menu alignment failed: " + line
            terminal.send(b"\x1b[B")
            pause(terminal, 0.4)
            terminal.send(b"\r")
            terminal.wait("Output: positive")
            terminal.wait("Enter or Esc: return to actions")
            pause(terminal, 1.6)
            evidence["checkpoints"]["results"] = time.monotonic()-terminal.started
            evidence["screens"]["results"] = harness.screen(terminal.raw, width=104, height=42).decode()
            assert "Batch: batch_partial_demo" in evidence["screens"]["results"].splitlines(), "result did not start on a clean line"
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("Batch: batch_partial_demo", after=offset)
            terminal.send(b"\x1b[B")
            pause(terminal, 0.4)
            terminal.send(b"\r")
            terminal.wait("Synthetic service error")
            terminal.wait("Enter or Esc: return to actions", after=offset)
            pause(terminal, 1.8)
            evidence["checkpoints"]["errors"] = time.monotonic()-terminal.started
            evidence["screens"]["errors"] = harness.screen(terminal.raw, width=104, height=42).decode()
            assert "Batch: batch_partial_demo" in evidence["screens"]["errors"].splitlines(), "error result did not start on a clean line"
            offset = len(terminal.raw)
            terminal.send(b"\r")
            terminal.wait("Batch: batch_partial_demo", after=offset)
            offset = len(terminal.raw)
            terminal.send(b"\x1b")
            terminal.wait("Search covers this page only", after=offset)
            pause(terminal, 0.4)
            terminal.send(b"\x1b")
            terminal.finish(0)
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
