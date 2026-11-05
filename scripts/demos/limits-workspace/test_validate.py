#!/usr/bin/env python3
"""Check quiet-tail timing without a terminal or renderer."""

import copy
import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location("limits_capture_validation", Path(__file__).with_name("validate.py"))
validator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validator)


class TimelineContracts(unittest.TestCase):
    def setUp(self):
        self.evidence = {"width": 80, "height": 24, "observation_end_seconds": 1.25,
                         "checkpoints": [{"name": "printed-command", "seconds": 1.0}]}
        self.child = [{"version": 2, "width": 80, "height": 24},
                      [0.25, "o", "\x1b[?25hprinted command\r\n"], [1.25, "o", ""]]

    def test_quiet_checkpoint_preserves_exact_terminal_bytes(self):
        validator.validate_child_timeline(self.evidence, self.child)
        raw = "".join(event[2] for event in self.child[1:] if event[1] == "o").encode()
        self.assertEqual(raw, b"\x1b[?25hprinted command\r\n")
        self.assertLess(self.child[1][0], self.evidence["checkpoints"][0]["seconds"])

    def test_missing_quiet_tail_is_rejected(self):
        with self.assertRaises(AssertionError):
            validator.validate_child_timeline(self.evidence, self.child[:-1])

    def test_checkpoint_beyond_observation_is_rejected(self):
        self.evidence["checkpoints"][0]["seconds"] = 1.251
        with self.assertRaises(AssertionError):
            validator.validate_child_timeline(self.evidence, self.child)

    def test_invalid_or_reversed_times_are_rejected(self):
        for location in ("end", "event", "checkpoint"):
            for value in (float("nan"), float("inf"), -0.1, True):
                with self.subTest(location=location, value=value):
                    evidence, child = copy.deepcopy(self.evidence), copy.deepcopy(self.child)
                    if location == "end":
                        evidence["observation_end_seconds"] = child[-1][0] = value
                    elif location == "event":
                        child[1][0] = value
                    else:
                        evidence["checkpoints"][0]["seconds"] = value
                    with self.assertRaises(AssertionError):
                        validator.validate_child_timeline(evidence, child)
        self.child.insert(2, [0.1, "o", "late event"])
        with self.assertRaises(AssertionError):
            validator.validate_child_timeline(self.evidence, self.child)

    def test_renderer_dimensions_must_match_observation(self):
        self.child[0]["width"] = 40
        with self.assertRaises(AssertionError):
            validator.validate_child_timeline(self.evidence, self.child)


if __name__ == "__main__":
    unittest.main(verbosity=2)
