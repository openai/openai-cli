#!/usr/bin/env python3
"""Relay authentic Images PTY output into the shared asciinema recorder."""

import argparse
import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import struct
import sys
import tempfile
import time
import zlib


def synthetic_png():
    # A deterministic geometric image. This is fixture data, not generated art.
    width, height = 120, 80
    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            sky = (35 + x // 3, 70 + y, 160 + x // 2)
            if (x - 88) ** 2 + (y - 22) ** 2 < 11 ** 2:
                sky = (247, 205, 117)
            if y > 44 + abs(x - 37) // 3:
                sky = (38, 112 + y // 2, 125)
            if y > 62 - abs(x - 96) // 6:
                sky = (25, 76 + y // 2, 105)
            row.extend(sky)
        rows.append(b"\0" + row)

    def chunk(kind, value):
        return struct.pack("!I", len(value)) + kind + value + struct.pack("!I", zlib.crc32(kind + value) & 0xffffffff)

    return (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack("!IIBBBBB", width, height, 8, 2, 0, 0, 0))
            + chunk(b"IDAT", zlib.compress(b"".join(rows))) + chunk(b"IEND", b""))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--baseline", action="store_true")
    args = parser.parse_args()
    repo, binary = args.repo.resolve(), args.binary.resolve(strict=True)
    args.output.mkdir(parents=True, exist_ok=False)
    spec = importlib.util.spec_from_file_location("demo_driver", repo / "scripts/image-picker-layout/drive_picker.py")
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    sys.path.insert(0, str(repo / "scripts/demos"))
    import image_recovery_check as recovery
    from image_picker_generation_check import screen

    recovery.PNG = base64.b64encode(synthetic_png()).decode()
    api = recovery.Fixture(delay=2.5)
    terminal = None
    checkpoints = []
    report = {"passed": False, "baseline": args.baseline,
              "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
              "fixture_sha256": hashlib.sha256(synthetic_png()).hexdigest(),
              "viewport": [110, 38], "capture": "actual PTY bytes; agg replay; no native graphical claim"}
    try:
        with tempfile.TemporaryDirectory(prefix="images-demo-") as temporary:
            home = Path(temporary)
            state = recovery.preference(home, "3")
            preferences = json.loads(state.read_text())
            preferences.update(size="1536x1024", quality="auto")
            state.write_text(json.dumps(preferences))
            inline = state.with_name("image-preferences.json")
            inline.write_text(json.dumps({"version": 1, "inline": True}))
            inline.chmod(0o600)
            env = recovery.environment(home, api)
            env.pop("NO_COLOR", None)
            env.update(COLORTERM="truecolor", OPENAI_PICKER_SHELL="bash", FORCE_COLOR="3", CI="")
            terminal = driver.RecordingTerminal(str(binary), ["images", "generate"], env,
                                                width=110, height=38, max_output_bytes=8*1024*1024,
                                                max_events=10000, max_event_bytes=10*1024*1024,
                                                deadline=time.monotonic()+55)

            def frame():
                return screen(bytes(terminal.raw), 110, 38)

            def wait(predicate, timeout=8):
                end = time.monotonic()+timeout
                while not predicate():
                    terminal.read(0.04)
                    if not terminal.running() or time.monotonic()>end:
                        raise AssertionError("scene condition timed out or CLI exited")

            def hold(seconds):
                driver.hold(terminal, seconds)

            def checkpoint(name):
                terminal.save(args.output / name)
                # A read can split UTF-8 between bytes. Keep checkpoint replays
                # exact instead of using the helper's per-chunk replacement decoder.
                header = dict(version=2, width=110, height=38,
                              title="Images checkpoint; synthetic fixtures")
                event = [0, "o", bytes(terminal.raw).decode("utf-8", "strict")]
                (args.output / (name+".cast")).write_text(
                    json.dumps(header)+"\n"+json.dumps(event)+"\n")
                (args.output / (name+".screen.txt")).write_bytes(frame())
                checkpoints.append({"name": name, "time": time.monotonic()-terminal.started,
                                    "bytes": len(terminal.raw), "requests": len(api.requests)})

            driver.picker.ready(terminal)
            terminal.send(b"\x1b[200~A calm geometric landscape\x1b[201~")
            terminal.wait("A calm geometric landscape")
            hold(1.6)
            checkpoint("form")
            terminal.send(b"\x07")
            wait(lambda: len(api.requests)==1)
            hold(1.1)
            checkpoint("generating")
            wait(lambda: len(recovery.saved(home))==3)
            if args.baseline:
                wait(lambda: b"All images saved." in frame())
                hold(1.4)
                checkpoint("results")
            else:
                wait(lambda: b"Saved in" in frame() and b"Generating image" not in frame())
                hold(1.1)
                checkpoint("results")
                terminal.send(b"\x1b[Z")  # Prompt to Images.
                hold(0.25)
                terminal.send(b"\x1b[H")
                hold(0.8)
                checkpoint("gallery-top")
                top = frame()
                for _ in range(12):
                    terminal.send(b"\x1b[B")
                    hold(0.09)
                checkpoint("gallery-scroll")
                assert frame() != top, "gallery arrow scrolling did not move its content"
                terminal.send(b"\x1b[F")
                hold(0.7)
                terminal.send(b"\x1b")
                hold(0.25)
                terminal.send(b"\x07")
                wait(lambda: len(api.requests)==2)
                wait(lambda: b"Generating image" in frame())
                assert "▀".encode() in frame(), "repeat generation removed existing previews"
                hold(1.2)
                checkpoint("retained-generating")
                wait(lambda: len(recovery.saved(home))==6)
                wait(lambda: b"Saved in" in frame() and b"Generating image" not in frame())
                hold(1.5)
                checkpoint("repeat-results")
            assert len(api.requests)==(1 if args.baseline else 2)
            assert all(request["prompt"]=="A calm geometric landscape" and request["n"]==3 for request in api.requests)
            checkpoint("final")
            terminal.send(b"\x03")
            terminal.finish(130)
            report.update(passed=True, requests=len(api.requests), saved_files=len(recovery.saved(home)),
                          exit_status=terminal.child.returncode, checkpoints=checkpoints)
    except BaseException as error:
        report["error"] = f"{type(error).__name__}: {error}"
        raise
    finally:
        try:
            if terminal is not None:
                try:
                    terminal.save(args.output / "complete")
                finally:
                    terminal.close()
        finally:
            api.close()
            (args.output / "REPORT.json").write_text(json.dumps(report, indent=2)+"\n")


if __name__ == "__main__":
    main()
