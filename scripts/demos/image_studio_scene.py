#!/usr/bin/env python3
"""Record the Images studio through the existing PTY driver and local fixture."""

import argparse
import base64
from contextlib import ExitStack
import hashlib
import importlib.util
import json
from pathlib import Path
import signal
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
import image_recovery_check as recovery
from image_workspace_scene import synthetic_png


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
    spec = importlib.util.spec_from_file_location("studio_driver", repo / "scripts/image-picker-layout/drive_picker.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    width, height = 120, 32
    prompt = "Alpine lake at dusk"
    recovery.PNG = base64.b64encode(synthetic_png()).decode()
    api = recovery.Fixture(delay=1.2)
    terminal = None
    report = {"passed": False, "baseline": args.baseline,
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "fixture_sha256": hashlib.sha256(synthetic_png()).hexdigest(),
              "viewport": [width, height], "live_api": False, "physical_terminal": False,
              "capture": "actual CLI bytes; color-block preview; shared asciinema/agg replay", "checkpoints": []}
    try:
        with ExitStack() as cleanup:
            temporary = cleanup.enter_context(tempfile.TemporaryDirectory(prefix="image-studio-demo-"))
            home = Path(temporary)
            preference = recovery.preference(home, "3")
            settings = json.loads(preference.read_text())
            settings.update(size="1536x1024", quality="high")
            preference.write_text(json.dumps(settings))
            inline = preference.with_name("image-preferences.json")
            inline.write_text(json.dumps({"version": 1, "inline": True}))
            inline.chmod(0o600)
            env = recovery.environment(home, api)
            env.pop("NO_COLOR")
            env.update(COLORTERM="truecolor", FORCE_COLOR="3", OPENAI_PICKER_SHELL="bash", CI="")
            terminal = driver.RecordingTerminal(str(binary), ["images", "generate"], env,
                                               width=width, height=height, max_output_bytes=8*1024*1024,
                                               max_events=10000, max_event_bytes=10*1024*1024,
                                               deadline=time.monotonic()+45)
            cleanup.callback(terminal.close)

            def frame():
                return driver.picker.screen(bytes(terminal.raw), width, height).decode()

            def wait(predicate, label):
                end = time.monotonic()+10
                while not predicate():
                    terminal.read(0.04)
                    if not terminal.running() or time.monotonic()>end:
                        raise AssertionError("scene did not reach " + label)

            def key(value, pause=0.35):
                terminal.send(value)
                driver.hold(terminal, pause)

            def checkpoint(name):
                # Decode the complete stream once, so a split UTF-8 write stays exact.
                cast = [{"version": 2, "width": width, "height": height, "title": "Synthetic Images studio"},
                        [0, "o", bytes(terminal.raw).decode("utf-8", "strict")]]
                (args.output / (name+".cast")).write_text("\n".join(json.dumps(event) for event in cast)+"\n")
                (args.output / (name+".screen.txt")).write_text(frame())
                report["checkpoints"].append({"name": name, "requests": len(api.requests),
                                               "bytes": len(terminal.raw)})

            driver.picker.ready(terminal)
            key(b"\x1b[200~" + prompt.encode() + b"\x1b[201~", 1.2)
            checkpoint("prompt")
            key(b"\t" if args.baseline else driver.picker.DOWN, 0.7)
            assert "Settings" in frame() and not api.requests
            checkpoint("settings")
            key(b"\x1b", 0.25)
            key(b"\x07", 0.45)
            wait(lambda: len(api.requests)==1, "one synthetic request")
            checkpoint("generating")
            receipt = "Saved in " if args.baseline else "Saved to "
            wait(lambda: len(list(home.rglob("*.png")))==3 and receipt in frame(), "three saved images")
            driver.hold(terminal, 0.7)
            assert len(recovery.saved(home))==3
            assert len(api.requests)==1 and api.requests[0]["prompt"]==prompt and api.requests[0]["n"]==3
            checkpoint("results")
            if not args.baseline:
                key(b"\t", 0.65)
                assert "Tab editor" in frame() and "1 / 3" in frame()
                key(b"\x1b[C", 0.75)
                assert "2 / 3" in frame() and "-2.png" in frame()
                assert "Saved to " in frame()
                checkpoint("images")
                key(b"\t", 0.65)
                assert "Alt+Enter newline" in frame() and prompt in frame()
                checkpoint("editor")
                key(b"\t", 0.7)
                assert "Tab editor" in frame() and len(api.requests)==1
                key(b"\x1b[D", 0.8)
                assert "1 / 3" in frame()
            else:
                key(b"\x1b[Z", 0.3)
                key(driver.picker.HOME, 1.2)
                assert "alpine-lake-at-dusk.png" in frame()
            checkpoint("final")
            terminal.send(b"\x03")
            terminal.finish(130)
            assert len(recovery.saved(home))==3 and len(api.requests)==1
            report.update(passed=True, requests=1, saved_images=3, exit_status=130, terminal_restored=True)
    finally:
        try:
            if terminal is not None:
                terminal.save(args.output / "complete")
        finally:
            api.close()
            report["processes_ended"] = terminal is None or terminal.child.returncode is not None
            (args.output / "REPORT.json").write_text(json.dumps(report, indent=2)+"\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
