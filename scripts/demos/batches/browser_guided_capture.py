#!/usr/bin/env python3
"""Capture guided creation, automatic progress, results, and saving through a real PTY."""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import time

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(Path(__file__).resolve().parent))
from workflow_fixture import OUTPUT_BYTES

spec = importlib.util.spec_from_file_location("guided_terminal", REPO / "scripts/image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


def settle(terminal, duration=0.25):
    until = time.monotonic() + duration
    while time.monotonic() < until:
        terminal.read(min(0.03, max(0, until - time.monotonic())))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("output", type=Path, help="New directory for retained replay and evidence")
    parser.add_argument("--launcher", type=Path, default=Path(__file__).with_name("try_browser.py"),
                        help="Synthetic launcher accepting the binary path")
    parser.add_argument("--launcher-takes-no-binary", action="store_true",
                        help="Launcher already selects its candidate binary")
    parser.add_argument("--width", type=int, default=110)
    parser.add_argument("--height", type=int, default=30)
    args = parser.parse_args()
    args.binary = args.binary.resolve(strict=True)
    args.launcher = args.launcher.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    header = {"version": 2, "width": args.width, "height": args.height,
              "title": "Guided Batch workflow; actual CLI with synthetic data", "env": {"TERM": "xterm-256color"}}
    evidence = {"binary": str(args.binary), "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                "launcher": str(args.launcher), "launcher_sha256": hashlib.sha256(args.launcher.read_bytes()).hexdigest(),
                "width": args.width, "height": args.height, "synthetic": True,
                "source": "Actual CLI frames from a macOS PTY", "screens": {}, "keys": [],
                "limits": "PTY replay does not establish physical terminal appearance or native Windows/Linux execution."}
    terminal = None
    try:
        with tempfile.TemporaryDirectory(prefix="f13-guided-capture-home-") as home:
            environment = {"PATH": os.defpath, "HOME": home, "XDG_CONFIG_HOME": home,
                           "XDG_CACHE_HOME": home, "TERM": "xterm-256color", "COLORTERM": "truecolor",
                           "COLORFGBG": "15;0", "LANG": "en_US.UTF-8", "PYTHONDONTWRITEBYTECODE": "1"}
            command = [str(args.launcher)]
            if not args.launcher_takes_no_binary:
                command.append(str(args.binary))
            terminal = harness.Terminal(sys.executable, command, environment,
                                        width=args.width, height=args.height, max_output_bytes=2_000_000,
                                        deadline=time.monotonic() + 100)

            def save(name):
                settle(terminal)
                screen = harness.screen(terminal.raw, args.width, args.height).decode()
                (args.output / (name + ".txt")).write_text(screen + "\n")
                (args.output / (name + ".cast")).write_text(
                    "\n".join(json.dumps(row) for row in [header, *terminal.events]) + "\n")
                evidence["screens"][name] = {"events": len(terminal.events), "text": screen}
                return screen

            def advance(keys, description, marker):
                at = len(terminal.raw)
                evidence["keys"].append(description)
                terminal.send(keys)
                wait_screen(marker, after=at)

            def wait_screen(marker, timeout=12, after=0):
                until = time.monotonic() + timeout
                while True:
                    terminal.read()
                    if len(terminal.raw) > after and marker in harness.screen(terminal.raw, args.width, args.height).decode():
                        settle(terminal)
                        return
                    if not terminal.running() or time.monotonic() > until:
                        raise AssertionError("Missing screen marker: " + marker)

            terminal.wait("Ctrl+N")
            save("01-batches")
            advance(b"\x0e", "Ctrl+N: create", "Review and create")
            save("02-create")
            advance(b"\r", "Enter: choose input", "Browse local files")
            advance(b"\r", "Enter: browse local files", "Choose a JSONL input file")
            save("03-local-files")
            terminal.send(b"requests.jsonl")
            evidence["keys"].append("Type requests.jsonl: filter local files")
            filtered = save("04-file-search")
            assert "requests.jsonl" in filtered
            advance(b"\r", "Enter: choose requests.jsonl", "Review and create")
            save("05-ready")
            advance(b"\x1b[B" * 3 + b"\r", "Down three, Enter: review", "Ctrl+S creates one batch")
            review = save("06-review")
            assert "requests.jsonl" in review and "/v1/responses" in review and "24 hours" in review
            advance(b"\x13", "Ctrl+S: confirm creation", "batch_created_demo")
            wait_screen("Updates every 10s")
            save("07-progress")
            waiting_at = len(terminal.raw)
            wait_screen("3 of 3 succeeded", timeout=40, after=waiting_at)
            completed = save("08-completed")
            assert "Completed" in completed
            evidence["automatic_progress_without_refresh"] = True

            advance(b"\r", "Enter: view results", "Ctrl+V")
            results = save("09-results")
            assert all(label in results for label in ("positive", "negative", "neutral"))
            advance(b"\r", "Enter: inspect one result", "PgUp/PgDn")
            save("10-result-details")
            advance(b"\x1b", "Esc: return to results", "Ctrl+V")
            advance(b"\x1b", "Esc: return to batch", "Save a file")
            advance(b"\x1b[B" * 3 + b"\r", "Down three, Enter: expand Save a file", "Output file")
            save("11-save-options")
            advance(b"\r", "Enter: choose output file", "Folder:")
            destination = save("12-save-location")
            filename = "batch_created_demo-output.jsonl"
            assert filename in destination and "Folder:" in destination
            advance(b"\r", "Enter: save output", "PgUp/PgDn")
            receipt = save("13-saved")
            assert filename in receipt
            advance(b"\x1b", "Esc: return to batch", "Save a file")
            advance(b"\x1b", "Esc: collapse Save a file", "Save a file")
            advance(b"\x1b", "Esc: return to batches", "Search")
            save("14-returned")
            terminal.send(b"\x1b")
            evidence["keys"].append("Esc: exit")
            terminal.finish(0, expect_alternate_screen=True)
            evidence["terminal_restored"] = True
            evidence["exit"] = terminal.child.returncode
            match = re.search(r"Demo downloads and details: ([^\r\n]+)", terminal.text())
            assert match, "launcher did not report its saved folder"
            directory = Path(match.group(1))
            session = json.loads((directory / "session.json").read_text())
            assert session["uploads"] == 1 and session["creates"] == 1
            assert not session["fixture_failures"] and session["server_stopped"] and session["process_stopped"]
            assert (directory / filename).read_bytes() == OUTPUT_BYTES
            compact_destination = "".join(line.strip(" │") for line in destination.splitlines())
            assert str(directory) in compact_destination, "the save screen did not show the output folder"
            requests = [json.loads(line) for line in (directory / session["request_log"]).read_text().splitlines()]
            assert [(r["method"], r["path"]) for r in requests if r["method"] == "POST"] == [
                ("POST", "/files"), ("POST", "/batches")]
            evidence.update(pass_=True, session=session, saved_file=str(directory / filename), requests=requests)
    except BaseException as error:
        evidence.update(pass_=False, failure=type(error).__name__, detail=str(error))
        raise
    finally:
        if terminal is not None:
            if terminal.running():
                terminal.send(b"\x03")
                until = time.monotonic() + 5
                while terminal.running() and time.monotonic() < until:
                    terminal.read(0.05)
            (args.output / "workflow.cast").write_text(
                "\n".join(json.dumps(row) for row in [header, *terminal.events]) + "\n")
            (args.output / "terminal.raw").write_bytes(bytes(terminal.raw))
            terminal.close()
        evidence["pass"] = evidence.pop("pass_", False)
        (args.output / "evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
    print(args.output)


if __name__ == "__main__":
    main()
