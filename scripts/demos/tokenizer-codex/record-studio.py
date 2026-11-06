#!/usr/bin/env python3
"""Record the same selected token in both editors, then edit it in the studio."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))
from image_picker_harness import Terminal, screen

DRAFT = "The blue door is beside the blue window."
WIDTH, HEIGHT = 120, 30
HOME_KEY, RIGHT = b"\x1b[H", b"\x1b[C"


def save_cast(path, events):
    header = {"version": 2, "width": WIDTH, "height": HEIGHT,
              "env": {"TERM": "xterm-256color"},
              "theme": {"fg": "#e2e2e2", "bg": "#1e222c",
                        "palette": "#000000:#aa0000:#00aa00:#aa5500:#0000aa:#aa00aa:#00aaaa:#aaaaaa:"
                                   "#555555:#ff5555:#55ff55:#ffff55:#5555ff:#ff55ff:#55ffff:#ffffff"}}
    with path.open("w", encoding="utf-8") as stream:
        for record in [header, *events]:
            stream.write(json.dumps(record, ensure_ascii=False) + "\n")


def capture(binary, name, output):
    with tempfile.TemporaryDirectory(prefix="tokenizer-demo-") as private:
        Path(private, "openai").symlink_to(binary)
        env = {"HOME": private, "PATH": private + os.pathsep + os.defpath,
               "PS1": "$ ", "HISTFILE": os.devnull, "BASH_SILENCE_DEPRECATION_WARNING": "1",
               "TERM": "xterm-256color", "COLORTERM": "truecolor", "COLORFGBG": "15;0",
               "LANG": "en_US.UTF-8", "OPENAI_BASE_URL": "http://127.0.0.1:1"}
        terminal = Terminal("/bin/bash", ["--noprofile", "--norc", "-i"], env,
                            width=WIDTH, height=HEIGHT, controlling_terminal=True)

        visible_rows = 0

        def press(keys, expected, hold=0.8):
            nonlocal visible_rows
            offset = len(terminal.raw)
            terminal.send(keys)
            terminal.wait(expected, after=offset)
            until = time.monotonic() + hold
            while time.monotonic() < until:
                terminal.read()
            # Interpret the one-cell DEC stroke when measuring the panel for cropping.
            raw = terminal.raw.replace(b"\x1b(0x\x1b(B", "│".encode()).replace(b"\x1b(B", b"")
            body = screen(raw, WIDTH, HEIGHT).rstrip()
            visible_rows = max(visible_rows, len(body.splitlines()))
            return body.decode("utf-8")

        try:
            terminal.wait("$ ")
            press(b"openai tokenizer\r", "Type or paste text")
            press(b"\x1b[200~" + DRAFT.encode() + b"\x1b[201~", "The blue door", 1.0)
            # The older editor puts Options between Text and Tokens in the Tab order.
            focused = press(b"\t", "Tokenizer")
            if "› Token " not in focused:
                press(b"\t", "Token ")
            press(HOME_KEY + RIGHT, "Token 2 of 9")
            press(RIGHT * 5, "Token 7 of 9", 1.4)
            save_cast(output / (name + ".cast"), terminal.events)
            if name == "after":
                press(b"\t", "› Text")
                press(b" bright", "the bright blue", 1.5)
                save_cast(output / "demo.cast", terminal.events)
            press(b"\x03", "$ ", 0)
            press(b"echo status=$?\r", "status=130", 0)
        finally:
            terminal.close()
        # The cast retains the whole viewport. Trim only unused lower rows from the media.
        return min(visible_rows + 1, HEIGHT)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("before", type=Path)
    parser.add_argument("after", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    if output.is_relative_to(Path(__file__).resolve().parents[3]):
        parser.error("Choose an output directory outside the repository.")
    outputs = ["before.cast", "after.cast", "demo.cast", "before.png", "after.png", "demo.gif"]
    if any((output / name).exists() for name in outputs):
        parser.error("Choose a directory without existing studio recordings.")
    agg, ffmpeg = shutil.which("agg"), shutil.which("ffmpeg")
    if not agg or not ffmpeg:
        parser.error("Add agg and ffmpeg to PATH before recording.")
    output.mkdir(parents=True, exist_ok=True)
    render = [agg, "--quiet", "--font-size", "16", "--line-height", "1.15",
              "--last-frame-duration", "1.5"]
    if os.environ.get("DEMO_FONT"):
        render.extend(["--font-family", os.environ["DEMO_FONT"]])
    with tempfile.TemporaryDirectory(prefix="tokenizer-render-") as temporary:
        still = Path(temporary, "selected.gif")
        for name, binary in [("before", args.before), ("after", args.after)]:
            rows = capture(binary.resolve(strict=True), name, output)
            crop = f"crop=iw:trunc(ih*{rows}/{HEIGHT}):0:0"
            subprocess.run([*render, "--select", "100%", output / (name + ".cast"), still], check=True)
            subprocess.run([ffmpeg, "-nostdin", "-loglevel", "error", "-y", "-i", still,
                            "-vf", crop, "-frames:v", "1", output / (name + ".png")], check=True)
        replay = Path(temporary, "replay.gif")
        subprocess.run([*render, output / "demo.cast", replay], check=True)
        palette = crop + ",split[a][b];[a]palettegen[p];[b][p]paletteuse=dither=none"
        subprocess.run([ffmpeg, "-nostdin", "-loglevel", "error", "-i", replay,
                        "-filter_complex", palette, "-vsync", "0", "-loop", "0",
                        "-final_delay", "150", output / "demo.gif"], check=True)


if __name__ == "__main__":
    main()
