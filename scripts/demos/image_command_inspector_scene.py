#!/usr/bin/env python3
"""Record command scrolling across pane switches through the actual Images CLI."""

import argparse
from contextlib import ExitStack
import hashlib
import importlib.util
import json
from pathlib import Path
import signal
import sys
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--baseline", action="store_true")
    args = parser.parse_args()
    for signum in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(signum, lambda received, _frame: sys.exit(128+received))
    repo = Path(__file__).resolve().parents[2]
    binary = args.binary.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    spec = importlib.util.spec_from_file_location("inspector_driver", repo / "scripts/image-picker-layout/drive_picker.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    prompt = "FIRST_VIEW " + " ".join(f"scene{i:02d} blue water and low clouds." for i in range(1, 42)) + " LAST_VIEW"
    width, height = 120, 32
    terminal = None
    home = None
    report = {"passed": False, "baseline": args.baseline,
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "prompt_sha256": hashlib.sha256(prompt.encode()).hexdigest(),
              "viewport": [width, height], "live_api": False, "api_fixture": False,
              "physical_terminal": False, "checkpoints": []}
    try:
        with ExitStack() as cleanup:
            home = Path(cleanup.enter_context(tempfile.TemporaryDirectory(prefix="image-inspector-demo-")))
            env = {"HOME": str(home), "PATH": "/usr/bin:/bin", "LANG": "en_US.UTF-8",
                   "TERM": "xterm-256color", "COLORTERM": "truecolor", "FORCE_COLOR": "3",
                   "OPENAI_API_KEY": "synthetic-inspector-key", "OPENAI_BASE_URL": "http://127.0.0.1:1/v1",
                   "OPENAI_CLI_NO_UPDATE_CHECK": "1", "OPENAI_PICKER_SHELL": "bash", "CI": "true"}
            terminal = driver.RecordingTerminal(str(binary), ["images", "generate"], env,
                                               width=width, height=height, max_output_bytes=2*1024*1024,
                                               max_events=2000, max_event_bytes=3*1024*1024,
                                               deadline=time.monotonic()+35)
            cleanup.callback(terminal.close)

            def frame():
                return driver.picker.screen(bytes(terminal.raw), width, height).decode()

            def key(value, pause=0.4):
                terminal.send(value)
                driver.hold(terminal, pause)

            def command_rows():
                # The accepted 120-column studio has 16 command rows in its left pane.
                return [line[4:49].rstrip() for line in frame().splitlines()[4:20]]

            def checkpoint(name):
                cast = [{"version": 2, "width": width, "height": height, "title": "Synthetic command inspector"},
                        [0, "o", bytes(terminal.raw).decode("utf-8", "strict")]]
                (args.output / (name+".cast")).write_text("\n".join(json.dumps(event) for event in cast)+"\n")
                (args.output / (name+".screen.txt")).write_text(frame())
                report["checkpoints"].append({"name": name, "bytes": len(terminal.raw)})

            until = time.monotonic()+10
            while "Create image" not in frame() or "Prompt" not in frame():
                terminal.read(0.04)
                if not terminal.running() or time.monotonic()>until:
                    raise AssertionError("prompt editor did not appear")
            key(b"\x1b[200~" + prompt.encode() + b"\x1b[201~", 0.8)
            key(driver.picker.HOME, 0.2)
            key(driver.picker.UP, 0.2)
            key(b"\x1b[C", 0.7)
            assert "› View command" in frame(), "View command must be selected before Enter"
            checkpoint("action")
            key(b"\r", 0.8)
            assert "Command" in frame() and "Ctrl+P print" in frame()
            for _ in range(3):
                key(driver.picker.DOWN, 0.25)
            before = command_rows()
            assert "FIRST_VIEW" in "\n".join(before) and "LAST_VIEW" not in "\n".join(before)
            driver.hold(terminal, 1.1)
            checkpoint("scrolled")
            key(b"\t", 1.5)
            switched = command_rows()
            assert "Command" in frame() and "Ctrl+P print" not in frame()
            if args.baseline:
                assert switched != before and "LAST_VIEW" in "\n".join(switched), "baseline must reproduce the jump"
            else:
                assert switched == before, "pane switching changed the rendered command rows"
            checkpoint("switched")
            key(b"\x1b[Z", 1.2)
            assert "Ctrl+P print" in frame() and command_rows() == before
            checkpoint("returned")
            terminal.send(b"\x03")
            terminal.finish(130)
            assert "Generating" not in terminal.text(), "inspection started image generation"
            assert not list(home.rglob("*.png")), "inspection created an image"
            report.update(passed=True, jump_observed=switched != before, command_rows_before=before,
                          command_rows_switched=switched, generation_started=False, saved_images=0,
                          exit_status=130, terminal_restored=True)
    finally:
        try:
            if terminal is not None:
                terminal.save(args.output / "complete")
        finally:
            report["processes_ended"] = terminal is None or terminal.child.returncode is not None
            report["temporary_home_removed"] = home is None or not home.exists()
            (args.output / "REPORT.json").write_text(json.dumps(report, indent=2)+"\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
