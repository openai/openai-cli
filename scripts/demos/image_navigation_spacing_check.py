#!/usr/bin/env python3
"""Capture public picker navigation using synthetic input and zero API requests.

Examples:
  python3 -B -I image_navigation_spacing_check.py BINARY OUTPUT --width 80
  python3 -B -I image_navigation_spacing_check.py BINARY OUTPUT --width 40

Each OUTPUT must be a new directory outside the repository. The capture includes
raw bytes, an asciicast, and byte-accurate checkpoints for terminal replay.
Use --relay with the shared capture_and_render.sh lifecycle for a visual demo.
"""

import argparse
import hashlib
import importlib.util
import json
import pathlib
import sys
import tempfile
import threading
import time


ROOT = pathlib.Path(__file__).resolve().parents[2]
DRIVER_PATH = ROOT / "scripts" / "image-picker-layout" / "drive_picker.py"
SPEC = importlib.util.spec_from_file_location("picker_layout_driver", DRIVER_PATH)
driver = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(driver)
picker = driver.picker


def capture(args):
    output = pathlib.Path(args.output).resolve()
    if output == ROOT or ROOT in output.parents:
        raise ValueError("keep recorded output outside the repository")
    output.mkdir(parents=True, exist_ok=False)
    binary = pathlib.Path(args.binary).resolve()
    height = 24 if args.width == 80 else 12
    checkpoints, actions = [], []
    server = picker.http.server.ThreadingHTTPServer(("127.0.0.1", 0), driver.RequestTrap)
    server.request_count = 0
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    report = {"status": "running"}
    try:
        with tempfile.TemporaryDirectory(prefix="image-navigation-spacing-") as home:
            environment = {
                "HOME": home,
                "PATH": "/usr/bin:/bin",
                "TERM": "xterm-256color",
                "LANG": "en_US.UTF-8",
                "OPENAI_API_KEY": "synthetic-navigation-key",
                "OPENAI_BASE_URL": f"http://127.0.0.1:{server.server_port}/v1",
                "OPENAI_PICKER_SHELL": "bash",
                "GOMAXPROCS": "2",
                "CI": "true",
            }
            if args.no_color:
                environment["NO_COLOR"] = "1"
            terminal_type = driver.RecordingTerminal if args.relay else picker.Terminal
            terminal = terminal_type(binary, ["images", "generate"], environment,
                                     width=args.width, height=height)

            def checkpoint(name, **expected):
                driver.hold(terminal, args.hold)
                assert server.request_count == 0, "navigation unexpectedly called the API"
                checkpoints.append({
                    "name": name,
                    "raw_bytes": len(terminal.raw),
                    "event_count": len(terminal.events),
                    "time": time.monotonic() - terminal.started,
                    "width": args.width,
                    "height": height,
                    "expected": expected,
                })

            def press(name, value, marker=None):
                after = len(terminal.raw)
                terminal.send(value)
                actions.append(name)
                if marker is not None:
                    terminal.wait(marker, after=after)
                else:
                    driver.hold(terminal, 0.08)

            try:
                picker.ready(terminal)
                press("paste synthetic prompt", b"\x1b[200~A tiny orange robot\x1b[201~",
                      "A tiny orange robot")
                checkpoint("initial-prompt", focus="prompt", page="settings")
                press("Down to Settings", picker.DOWN, "\u203a Model")
                checkpoint("settings-model", focus="options", selected="Model")
                press("End to More options", picker.END, "\u203a More options")
                press("Enter More options", b"\r", "File type")
                press("Enter Choose format", b"\r", "Choose format")
                checkpoint("format-png", checked="PNG", selected="PNG")
                press("Down to JPEG", picker.DOWN, "\u203a JPEG")
                checkpoint("format-jpeg-highlighted", checked="PNG", selected="JPEG")
                press("Enter to commit JPEG", b"\r", "File type")
                press("Enter to reopen Choose format", b"\r", "Choose format")
                checkpoint("format-jpeg-checked", checked="JPEG", selected="JPEG")
                press("Down to WEBP", picker.DOWN, "\u203a WEBP")
                checkpoint("format-webp-highlighted", checked="JPEG", selected="WEBP")
                press("Esc to prompt", b"\x1b", "Settings")
                checkpoint("prompt-after-format", focus="prompt", page="settings")
                press("Down to Settings", picker.DOWN, "\u203a Model")
                press("Down to Images", picker.DOWN * 3, "\u203a Images")
                press("Enter Choose image count", b"\r", "Choose image count")
                press("End to 10", picker.END, "\u203a 10")
                checkpoint("count-end", selected="10", checked="1")
                for number in (9, 8, 7):
                    press(f"Up to {number}", picker.UP, f"\u203a {number}")
                    checkpoint(f"count-up-{number}", selected=str(number), checked="1")
                press("Down to 8", picker.DOWN, "\u203a 8")
                checkpoint("count-down-8", selected="8", checked="1")
                press("six rapid Up/Down pairs", (picker.UP + picker.DOWN) * 6)
                checkpoint("count-rapid-return-8", selected="8", checked="1")
                press("Down to 10", picker.DOWN * 2, "\u203a 10")
                checkpoint("count-return-end", selected="10", checked="1")
                press("Esc to prompt", b"\x1b", "Settings")
                checkpoint("final-prompt", focus="prompt", count="1", format="JPEG")
                press("Down to Settings", picker.DOWN, "\u203a Model")
                checkpoint("final-settings", focus="options", selected="Model")
                press("Up to prompt", picker.UP)
                checkpoint("final-return-prompt", focus="prompt", count="1", format="JPEG")
                terminal.send(b"\x03")
                actions.append("Ctrl+C")
                terminal.finish(130)
                assert server.request_count == 0, "navigation made an API request"
                preferences = list(pathlib.Path(home).rglob("image-picker.json"))
                if args.baseline:
                    assert not preferences, "baseline cancellation wrote picker preferences"
                else:
                    assert len(preferences) == 1, "edited cancellation did not save one draft"
                    path = preferences[0]
                    assert path.stat().st_mode & 0o777 == 0o600, "draft is not private"
                    state = json.loads(path.read_text())
                    assert state == dict(version=2, prompt="A tiny orange robot",
                                         model="gpt-image-2.5-sunburst", size="1024x1024",
                                         quality="auto", background="auto", format="jpeg",
                                         count="1", output_dir=""), "draft contains unexpected fields or unconfirmed choices"
                report = {
                    "status": "public lifecycle passed; screen assertions require terminal replay",
                    "binary": str(binary),
                    "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
                    "width": args.width,
                    "height": height,
                    "baseline": args.baseline,
                    "no_color": args.no_color,
                    "api_requests": server.request_count,
                    "preferences_written": bool(preferences),
                    "exit_status": terminal.child.returncode,
                    "terminal_restored": True,
                    "checkpoints": checkpoints,
                    "actions": actions,
                }
            finally:
                try:
                    terminal.save(output / "navigation")
                    (output / "checkpoints.json").write_text(json.dumps(checkpoints, indent=2) + "\n")
                finally:
                    terminal.close()
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)
        if worker.is_alive():
            raise RuntimeError("navigation request trap did not stop")
        (output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    return 130 if args.relay else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("output")
    parser.add_argument("--width", type=int, choices=(40, 80), required=True)
    parser.add_argument("--baseline", action="store_true", help="expect the pre-draft cancellation behavior")
    parser.add_argument("--no-color", action="store_true")
    parser.add_argument("--relay", action="store_true")
    parser.add_argument("--hold", type=float, default=0.15)
    args = parser.parse_args()
    if not 0 <= args.hold <= 3:
        parser.error("--hold must be between 0 and 3 seconds")
    return capture(args)


if __name__ == "__main__":
    sys.exit(main())
