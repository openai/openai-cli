#!/usr/bin/env python3
"""Check viewport attributes and real renderer behavior across shrink and enlargement."""

import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
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

    def test_static_cast_preserves_checkpoint_and_observation_times(self):
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
        pixels = subprocess.check_output([self.ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error", "-i", str(gif),
                                          "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-"],
                                         stdin=subprocess.DEVNULL, timeout=15)
        return hashlib.sha256(pixels).hexdigest(), gif.read_bytes()[6:10]

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

    def test_public_export_preserves_late_checkpoint_after_initial_quiet(self):
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            events = [[13, "o", "\x1b[?25lquiet checkpoint"], [13, "o", ""]]
            renderer.write_cast(directory / "sample-viewport.cast", 40, 12, events, {})
            renderer.write_cast(directory / "sample-exit-viewport.cast", 40, 12, events, {})
            manifest = {"scenes": [{"scene": "sample", "animation": "sample-viewport.cast",
                         "checkpoints": [{"name": "exit", "seconds": 13, "cast": "sample-exit-viewport.cast"}]}]}
            renderer.render(directory, manifest, self.agg, self.ffmpeg)
            self.assertTrue((directory / "sample.png").stat().st_size)
            self.assertTrue((directory / "sample-frame.gif").stat().st_size)


if __name__ == "__main__":
    unittest.main(verbosity=2)
