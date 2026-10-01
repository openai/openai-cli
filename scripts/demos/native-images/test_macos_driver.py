"""Discovery contract tests only; never starts a GUI or capture process."""
import importlib.util
import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("macos_driver", Path(__file__).with_name("macos_driver.py"))
driver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(driver)


class WindowDiscovery(unittest.TestCase):
    def setUp(self):
        self.process = Mock(pid=1234)
        self.process.poll.return_value = None
        self.now = 0
        self.clock = patch.object(driver.time, "monotonic", side_effect=lambda: self.now)
        self.sleep = patch.object(driver.time, "sleep", side_effect=self.advance)
        self.clock.start()
        self.sleep.start()
        self.addCleanup(self.clock.stop)
        self.addCleanup(self.sleep.stop)

    def advance(self, seconds):
        self.now += seconds

    def missing(self, count=0, **changes):
        diagnostic = {"pid": 1234, "eligible_owned_count": count}
        diagnostic.update(changes)
        return subprocess.CalledProcessError(4, ["window-info"], stderr=json.dumps(diagnostic).encode())

    def found(self, **changes):
        window = {"pid": 1234, "window_id": 29, "bounds": {"Width": 964, "Height": 674}}
        window.update(changes)
        return subprocess.CompletedProcess([], 0, json.dumps(window).encode(), b"")

    def test_waits_for_delayed_owned_window(self):
        with patch.object(driver, "run", side_effect=[self.missing(), self.missing(), self.found()]) as run:
            self.assertEqual(driver.wait_owned_window("tool", self.process, {})["window_id"], 29)
            self.assertEqual(run.call_count, 3)
            self.assertAlmostEqual(self.now, 0.2)
            self.assertEqual(run.call_args.args[0], ["tool", "window", "1234"])
            self.assertLess(run.call_args.kwargs["timeout"], 4)

    def test_permanent_absence_is_bounded_and_retains_diagnostic(self):
        error = self.missing()
        with patch.object(driver, "run", side_effect=error):
            with self.assertRaises(subprocess.CalledProcessError) as caught:
                driver.wait_owned_window("tool", self.process, {}, seconds=0.25)
        self.assertIs(caught.exception, error)
        self.assertAlmostEqual(self.now, 0.25)

    def test_ambiguous_or_malformed_discovery_does_not_retry(self):
        malformed = subprocess.CalledProcessError(4, [], stderr=b"not JSON")
        for error in [self.missing(2), self.missing(1), self.missing(-1),
                      self.missing(False), self.missing(pid=4321), malformed]:
            with self.subTest(error=error.stderr), patch.object(driver, "run", side_effect=error) as run:
                with self.assertRaises(subprocess.CalledProcessError):
                    driver.wait_owned_window("tool", self.process, {})
                self.assertEqual(run.call_count, 1)
                self.assertEqual(self.now, 0)

    def test_permission_and_other_errors_do_not_retry(self):
        for status in [2, 3, 5, 6, -15]:
            error = subprocess.CalledProcessError(status, [], stderr=b"fatal")
            with self.subTest(status=status), patch.object(driver, "run", side_effect=error) as run:
                with self.assertRaises(subprocess.CalledProcessError):
                    driver.wait_owned_window("tool", self.process, {})
                self.assertEqual(run.call_count, 1)

    def test_query_timeout_is_fatal(self):
        with patch.object(driver, "run", side_effect=subprocess.TimeoutExpired([], 4)) as run:
            with self.assertRaises(subprocess.TimeoutExpired):
                driver.wait_owned_window("tool", self.process, {})
            self.assertEqual(run.call_count, 1)

    def test_exit_before_discovery_never_queries_recycled_pid(self):
        self.process.poll.return_value = 0
        with patch.object(driver, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "terminal exited before"):
                driver.wait_owned_window("tool", self.process, {})
            run.assert_not_called()

    def test_exit_during_discovery_never_returns_window(self):
        self.process.poll.side_effect = [None, 0]
        with patch.object(driver, "run", return_value=self.found()):
            with self.assertRaisesRegex(RuntimeError, "terminal exited during"):
                driver.wait_owned_window("tool", self.process, {})

    def test_success_requires_owned_valid_identity(self):
        for changes in [{"pid": 4321}, {"window_id": 0}, {"window_id": True}, {"window_id": "29"}]:
            with self.subTest(changes=changes), patch.object(driver, "run", return_value=self.found(**changes)):
                with self.assertRaisesRegex(RuntimeError, "invalid owned window identity"):
                    driver.wait_owned_window("tool", self.process, {})

    def test_capture_deadline_is_shared_and_fails_before_more_work(self):
        self.assertEqual(driver.capture_time_left(11), 11)
        self.advance(4.5)
        self.assertEqual(driver.capture_time_left(11), 6.5)
        self.advance(6.5)
        with self.assertRaisesRegex(RuntimeError, "checkpoint deadline"):
            driver.capture_time_left(11)


if __name__ == "__main__":
    unittest.main()
