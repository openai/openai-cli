#!/usr/bin/env python3
"""Check viewport attributes and real renderer behavior across shrink and enlargement."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


spec = importlib.util.spec_from_file_location("limits_viewport_export", Path(__file__).with_name("render_viewports.py"))
renderer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(renderer)


class ViewportContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.driver = renderer.driver_module()

    def test_default_named_bright_and_rgb_colors(self):
        self.assertEqual(renderer.color_codes("default"), [39])
        self.assertEqual(renderer.color_codes("default", True), [49])
        self.assertEqual(renderer.color_codes("brown"), [33])
        self.assertEqual(renderer.color_codes("brightblue", True), [104])
        self.assertEqual(renderer.color_codes("8ebaff"), [38, 2, 142, 186, 255])
        with self.assertRaises(AssertionError):
            renderer.color_codes("invalid-color")

    def test_styles_wide_cells_and_combining_marks_round_trip(self):
        original = self.driver.TerminalObserver(20, 4, lambda data: None)
        original.feed("\x1b[1;3;4;5;7;9;38;2;142;186;255;104m界e\u0301\x1b[0m".encode())
        projected = self.driver.TerminalObserver(20, 4, lambda data: None)
        projected.feed(renderer.viewport_bytes(original.screen).encode())
        self.assertEqual(projected.text(), original.text())
        for column in range(20):
            self.assertEqual(projected.screen.buffer[0][column], original.screen.buffer[0][column])

    def test_cursor_visibility_and_position_round_trip(self):
        for hidden in (False, True):
            with self.subTest(hidden=hidden):
                original = self.driver.TerminalObserver(20, 4, lambda data: None)
                original.feed(b"\x1b[3;7H")
                original.screen.cursor.hidden = hidden
                projected = self.driver.TerminalObserver(20, 4, lambda data: None)
                projected.feed(renderer.viewport_bytes(original.screen).encode())
                self.assertEqual((projected.screen.cursor.x, projected.screen.cursor.y, projected.screen.cursor.hidden),
                                 (6, 2, hidden))

    def test_letterbox_clears_prior_cells_without_scaling(self):
        original = self.driver.TerminalObserver(20, 4, lambda data: None)
        original.feed(b"\x1b[?25lshort")
        projected = self.driver.TerminalObserver(40, 12, lambda data: None)
        projected.feed(b"stale content\x1b[10;30Hstale")
        projected.feed(renderer.viewport_bytes(original.screen).encode())
        self.assertEqual(projected.screen.display[0], "short".ljust(40))
        self.assertTrue(all(not line.strip() for line in projected.screen.display[1:]))

    def test_cast_keeps_explicit_times_and_rejects_replacement(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "frame.cast"
            events = [[1.5, "o", "payload"], [2.25, "o", ""]]
            renderer.write_cast(path, 40, 12, events, {"seconds": 1.5})
            actual = [json.loads(line) for line in path.read_text().splitlines()]
            self.assertEqual((actual[0]["width"], actual[0]["height"]), (40, 12))
            self.assertEqual(actual[1:], events)
            with self.assertRaises(AssertionError):
                renderer.write_cast(path, 40, 12, events, {})


class ActualRendererResizeContracts(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.driver = renderer.driver_module()
        cls.agg = os.environ["LIMITS_DEMO_AGG"]
        cls.ffmpeg = os.environ["LIMITS_DEMO_FFMPEG"]

    def render(self, directory, name, size, events):
        cast, gif = directory / (name + ".cast"), directory / (name + ".gif")
        renderer.write_cast(cast, *size, events, {})
        subprocess.run([self.agg, "--quiet", "--renderer", "resvg", "--font-family", "Menlo", "--font-size", "18",
                        "--line-height", "1.2", "--theme", "asciinema", "--select", "2", str(cast), str(gif)],
                       check=True, stdin=subprocess.DEVNULL, timeout=15)
        return self.pixel_hash(gif), gif.read_bytes()[6:10]

    def pixel_hash(self, path):
        pixels = subprocess.check_output([self.ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", str(path),
                                          "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-"],
                                         stdin=subprocess.DEVNULL, timeout=15)
        return hashlib.sha256(pixels).hexdigest()

    def test_shrink_and_enlarge_checkpoints_match_independent_pixels(self):
        for initial, target in (((80, 24), (40, 12)), ((40, 12), (80, 24))):
            with self.subTest(initial=initial, target=target), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                payload = "\x1b[?25l\x1b[0m\x1b[2J\x1b[H\x1b[38;2;142;186;255mResize correct\x1b[0m"
                observer = self.driver.TerminalObserver(*initial, lambda data: None)
                observer.feed(b"old content that must disappear")
                observer.resize(*target)
                observer.feed(payload.encode())
                actual = self.render(directory, "projected", target, [[1, "o", renderer.viewport_bytes(observer.screen)], [3, "o", ""]])
                expected = self.render(directory, "expected", target, [[1, "o", payload], [3, "o", ""]])
                legacy = self.render(directory, "legacy-fixed-grid", initial,
                                     [[0.1, "o", "old content"], [0.5, "r", f"{target[0]}x{target[1]}"], [1, "o", payload], [3, "o", ""]])
                self.assertEqual(actual, expected)
                self.assertNotEqual(legacy[1], expected[1])

    def test_shrink_and_enlarge_animations_preserve_wrapping_and_letterbox(self):
        for initial, target in (((80, 24), (40, 12)), ((40, 12), (80, 24))):
            with self.subTest(initial=initial, target=target), tempfile.TemporaryDirectory() as temporary:
                directory = Path(temporary)
                prefix = "\x1b[?25l\x1b[0m\x1b[2J\x1b[H\x1b[38;2;142;186;255m"
                column = target[0] - 5
                observer = self.driver.TerminalObserver(*initial, lambda data: None)
                observer.feed(b"old viewport")
                observer.resize(*target)
                observer.feed((prefix + f"\x1b[1;{column}H" + "X" * 10 + "\x1b[0m").encode())
                expected = prefix + f"\x1b[1;{column}H" + "X" * 6 + "\x1b[2;1H" + "X" * 4 + "\x1b[0m"
                actual = self.render(directory, "animation-projection", (80, 24),
                                     [[1, "o", renderer.viewport_bytes(observer.screen)], [3, "o", ""]])
                self.assertEqual(actual, self.render(directory, "independent-wrap", (80, 24), [[1, "o", expected], [3, "o", ""]]))

    def test_public_export_fractional_late_and_exit_checkpoints_match_pixels(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            # Fractional values reproduce timestamps from the rejected synthetic exports.
            cases = ((0.9312871660222299, 5.30981912498828),
                     (0.9358217500266619, 7.4997836670372635), (13.25, 15.125))
            payloads, times = {}, {}
            for index, scene in enumerate(renderer.SCENES):
                checkpoint, end = cases[index % len(cases)]
                payload = "\x1b[?25l\x1b[0m\x1b[2J\x1b[Hcheckpoint " + scene
                payloads[scene], times[scene] = payload, (checkpoint, end)
                observer = self.driver.TerminalObserver(40, 12, lambda data: None)
                observer.feed(payload.encode())
                evidence = {"width": 40, "height": 12, "observation_end_seconds": end,
                            "checkpoints": [{"name": "overview", "seconds": checkpoint}]}
                (directory / (scene + "-evidence.json")).write_text(json.dumps(evidence))
                child = [{"version": 2, "width": 40, "height": 12}, [0.1, "o", payload], [end, "o", ""]]
                (directory / (scene + "-evidence.child.cast")).write_text("\n".join(map(json.dumps, child)) + "\n")
                for point in ("overview", "exit"):
                    (directory / (scene + "-evidence." + point + ".txt")).write_text(observer.text())
            originals = {path.name: hashlib.sha256(path.read_bytes()).hexdigest() for path in directory.iterdir()}
            subprocess.run([sys.executable, "-I", "-B", str(Path(renderer.__file__)), str(directory),
                            "--agg", self.agg, "--ffmpeg", self.ffmpeg],
                           check=True, stdin=subprocess.DEVNULL, timeout=30)
            self.driver.validator.validate_viewport_projections(directory)
            manifest = json.loads((directory / "viewport-manifest.json").read_text())
            self.assertEqual(manifest["static_render_timeline"], {"output_seconds": 0, "selection_seconds": 1, "end_seconds": 2})
            for entry in manifest["scenes"]:
                scene = entry["scene"]
                self.assertEqual(tuple(point["seconds"] for point in entry["checkpoints"]), times[scene])
                expected = self.render(directory, scene + "-independent", (40, 12),
                                       [[1, "o", payloads[scene]], [3, "o", ""]])[0]
                for name in (scene + "-overview.png", scene + ".png"):
                    with self.subTest(scene=scene, frame=name):
                        self.assertEqual(self.pixel_hash(directory / name), expected)
            for name, expected in originals.items():
                self.assertEqual(hashlib.sha256((directory / name).read_bytes()).hexdigest(), expected)


if __name__ == "__main__":
    unittest.main(verbosity=2)
