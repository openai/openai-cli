#!/usr/bin/env python3
"""Render observer-derived viewports while preserving immutable child captures."""

import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess


COLORS = ("black", "red", "green", "brown", "blue", "magenta", "cyan", "white")
SCENES = ("org-limit", "org-limit-delete", "project-limit", "alert-create", "alert-edit",
          "alert-delete", "rate-update", "uncertain", "slow-cancel", "alert-print", "preflight-400", "preflight-recover")


def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def color_codes(value, background=False):
    if value == "default":
        return [49 if background else 39]
    if value in COLORS:
        return [(40 if background else 30) + COLORS.index(value)]
    if value.startswith("bright") and value[6:] in COLORS:
        return [(100 if background else 90) + COLORS.index(value[6:])]
    assert re.fullmatch("[0-9a-fA-F]{6}", value), ("unsupported observed color", value)
    return [48 if background else 38, 2, *bytes.fromhex(value)]


def cell_style(cell):
    codes = [0, *color_codes(cell.fg), *color_codes(cell.bg, True)]
    for field, code in (("bold", 1), ("italics", 3), ("underscore", 4),
                        ("blink", 5), ("reverse", 7), ("strikethrough", 9)):
        if getattr(cell, field):
            codes.append(code)
    return "\x1b[" + ";".join(map(str, codes)) + "m"


def viewport_bytes(screen):
    """Serialize observed cells, including wide-cell placeholders and combining marks."""
    output = ["\x1b[?25l\x1b[0m\x1b[2J\x1b[H"]
    style = None
    for row in range(screen.lines):
        output.append(f"\x1b[{row + 1};1H")
        for column in range(screen.columns):
            cell = screen.buffer[row][column]
            if not cell.data:
                continue  # A wide glyph already occupies this continuation cell.
            wanted = cell_style(cell)
            if wanted != style:
                output.append(wanted)
                style = wanted
            output.append(cell.data)
    output.append("\x1b[0m")
    output.append(f"\x1b[{screen.cursor.y + 1};{min(screen.cursor.x, screen.columns - 1) + 1}H")
    output.append("\x1b[?25l" if screen.cursor.hidden else "\x1b[?25h")
    return "".join(output)


def write_cast(path, width, height, events, provenance):
    assert not path.exists() and not path.is_symlink(), ("use a fresh rendering directory", path)
    header = {"version": 2, "width": width, "height": height,
              "title": "Observer-derived viewport; original captured bytes remain separate",
              "limits_viewport": provenance}
    path.write_text("\n".join(json.dumps(item, ensure_ascii=False) for item in [header, *events]) + "\n")


def driver_module():
    spec = importlib.util.spec_from_file_location("limits_viewport_driver", Path(__file__).with_name("scene.py"))
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    return driver


def prepare(root):
    driver = driver_module()
    captures = []
    for scene in SCENES:
        evidence_path = root / (scene + "-evidence.json")
        child_path = root / (scene + "-evidence.child.cast")
        evidence = json.loads(evidence_path.read_text())
        child = [json.loads(line) for line in child_path.read_text().splitlines()]
        driver.validator.validate_child_timeline(evidence, child)
        captures.append((scene, evidence_path, child_path, evidence, child))
    sizes = [(item[3]["width"], item[3]["height"]) for item in captures]
    sizes += [tuple(map(int, event[2].split("x"))) for item in captures for event in item[4][1:] if event[1] == "r"]
    canvas = (max(size[0] for size in sizes), max(size[1] for size in sizes))
    manifest = {"renderer_sha256": digest(__file__), "observer_sha256": digest(Path(__file__).with_name("scene.py")),
                "animation_canvas": list(canvas), "projection": "observer-derived, fixed-font, letterboxed replay",
                "limitation": "Offline viewport model; resize clips without terminal reflow.", "scenes": []}
    for scene, evidence_path, child_path, evidence, child in captures:
        source = {"child_cast_sha256": digest(child_path), "evidence_sha256": digest(evidence_path),
                  "renderer_sha256": manifest["renderer_sha256"]}
        observer = driver.TerminalObserver(evidence["width"], evidence["height"], lambda data: None)
        points = iter(evidence["checkpoints"])
        point = next(points, None)
        frames, frame_sources, entries = [], [], []

        def snapshot(name, seconds, expected_path):
            assert re.fullmatch("[a-z0-9-]+", name), "invalid checkpoint name"
            assert observer.text() == expected_path.read_text(), (scene, name, "source viewport differs")
            width, height = observer.screen.columns, observer.screen.lines
            path = root / (scene + "-" + name + "-viewport.cast")
            provenance = {**source, "checkpoint": name, "seconds": seconds, "active_size": [width, height],
                          "snapshot_sha256": digest(expected_path)}
            write_cast(path, width, height, [[seconds, "o", viewport_bytes(observer.screen)],
                       [evidence["observation_end_seconds"], "o", ""]], provenance)
            entries.append({"name": name, "seconds": seconds, "active_size": [width, height],
                            "cast": path.name, "sha256": digest(path), "source": provenance})

        for event_index, event in enumerate(child[1:], 1):
            while point is not None and point["seconds"] < event[0]:
                snapshot(point["name"], point["seconds"], root / (scene + "-evidence." + point["name"] + ".txt"))
                point = next(points, None)
            if event[1] == "r":
                observer.resize(*map(int, event[2].split("x")))
            else:
                observer.feed(event[2].encode())
            if event[1] == "r" or event[2]:
                frames.append([event[0], "o", viewport_bytes(observer.screen)])
                frame_sources.append({"source_event_index": event_index, "seconds": event[0],
                                      "active_size": [observer.screen.columns, observer.screen.lines]})
        while point is not None:
            snapshot(point["name"], point["seconds"], root / (scene + "-evidence." + point["name"] + ".txt"))
            point = next(points, None)
        snapshot("exit", evidence["observation_end_seconds"], root / (scene + "-evidence.exit.txt"))
        animation = root / (scene + "-viewport.cast")
        frames.append([evidence["observation_end_seconds"], "o", ""])
        write_cast(animation, *canvas, frames, {**source, "projection": manifest["projection"]})
        manifest["scenes"].append({"scene": scene, "source": source, "animation": animation.name,
                                   "animation_sha256": digest(animation), "animation_events": frame_sources,
                                   "checkpoints": entries})
    (root / "viewport-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return manifest


def render(root, manifest, agg, ffmpeg):
    # Derived stills retain absolute checkpoint times, including their initial quiet interval.
    observation_end = max(point["seconds"] for entry in manifest["scenes"] for point in entry["checkpoints"])
    options = [agg, "--quiet", "--renderer", "resvg", "--font-family", "Menlo", "--font-size", "18",
               "--line-height", "1.2", "--theme", "asciinema", "--fps-cap", "15", "--last-frame-duration", "2",
               "--idle-time-limit", str(observation_end)]

    def run(argv):
        subprocess.run(argv, check=True, stdin=subprocess.DEVNULL, timeout=30)

    rows = []
    for entry in manifest["scenes"]:
        scene = entry["scene"]
        run([*options, str(root / entry["animation"]), str(root / (scene + ".gif"))])
        for point in entry["checkpoints"]:
            base = scene if point["name"] == "exit" else scene + "-" + point["name"]
            frame, png = base + "-frame.gif", base + ".png"
            run([*options, "--select", str(point["seconds"]), str(root / point["cast"]), str(root / frame)])
            run([ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", str(root / frame),
                 "-frames:v", "1", str(root / png)])
            if point["name"] != "exit":
                rows.append("\t".join((scene, point["name"], str(point["seconds"]), frame, png)))
    (root / "checkpoint-frames.txt").write_text("\n".join(rows) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--agg", required=True)
    parser.add_argument("--ffmpeg", required=True)
    args = parser.parse_args()
    manifest = prepare(args.directory)
    render(args.directory, manifest, args.agg, args.ffmpeg)


if __name__ == "__main__":
    main()
