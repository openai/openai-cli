"""Offline checks for pinned shell archive recovery and reuse."""
import hashlib
import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("download_shells", Path(__file__).with_name("download_minimum_shells.py"))
download_shells = importlib.util.module_from_spec(spec)
spec.loader.exec_module(download_shells)


class DownloadTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)
        self.body = b"synthetic archive contents\n"
        self.digest = hashlib.sha256(self.body).hexdigest()
        self.destination = self.directory / "shell.tar.gz"
        self.calls = []
        self.timeouts = []
        self.clock = 0
        self.enterContext(patch.object(download_shells.time, "monotonic", side_effect=lambda: self.clock))
        self.sleeps = self.enterContext(patch.object(download_shells.time, "sleep", side_effect=self.advance_clock))

    def advance_clock(self, seconds):
        self.clock += seconds

    def run_download(self, outcomes, deadline=1000):
        remaining = iter(outcomes)

        def curl(command, **kwargs):
            self.calls.append(command)
            self.assertEqual(command[:2], ["curl", "--disable"])
            for flag, value in [("--proto", "=https"), ("--proto-redir", "=https"),
                                ("--connect-timeout", "15")]:
                self.assertEqual(command[command.index(flag) + 1], value)
            self.assertIn("--fail", command)
            self.assertFalse(kwargs["check"])
            self.assertLessEqual(kwargs["timeout"], min(95, deadline - self.clock))
            self.assertLessEqual(float(command[command.index("--max-time") + 1]), min(90, deadline - self.clock))
            self.timeouts.append(kwargs["timeout"])
            target = Path(command[command.index("--output") + 1])
            self.assertNotEqual(target, self.destination)
            code, body, *elapsed = next(remaining)
            target.write_bytes(body)
            if elapsed:
                self.advance_clock(elapsed[0])
            if code == "timeout":
                raise subprocess.TimeoutExpired(command, 95)
            return subprocess.CompletedProcess(command, code)

        with patch.object(download_shells.subprocess, "run", side_effect=curl):
            download_shells.download(self.directory, self.destination.name, "https://example.test/archive", self.digest, deadline)

    def assert_no_partial_files(self):
        self.assertEqual(list(self.directory.glob(".shell.tar.gz.*")), [])

    def test_verified_cache_needs_no_network(self):
        self.destination.write_bytes(self.body)
        self.run_download([])
        self.assertEqual(self.calls, [])
        self.sleeps.assert_not_called()

    def test_corrupt_cache_is_replaced_only_after_verification(self):
        self.destination.write_bytes(b"corrupt cache")
        self.run_download([(0, self.body)])
        self.assertEqual(self.destination.read_bytes(), self.body)
        self.assertEqual(len(self.calls), 1)
        self.assert_no_partial_files()

    def test_transient_failures_retry_with_fresh_files(self):
        self.run_download([(28, b"partial"), ("timeout", b"other partial"), (0, self.body)])
        self.assertEqual(self.destination.read_bytes(), self.body)
        self.assertEqual(len(self.calls), 3)
        targets = [c[c.index("--output") + 1] for c in self.calls]
        self.assertEqual(len(set(targets)), 3)
        self.assertEqual([c.args for c in self.sleeps.call_args_list], [(1,), (2,)])
        self.assert_no_partial_files()

    def test_exhausted_retries_fail_without_publishing_partial_bytes(self):
        with self.assertRaisesRegex(RuntimeError, "curl exit 28"):
            self.run_download([(28, b"partial")] * 3)
        self.assertEqual(len(self.calls), 3)
        self.assertFalse(self.destination.exists())
        self.assert_no_partial_files()

    def test_checksum_mismatch_fails_without_retry_or_replacement(self):
        self.destination.write_bytes(b"old corrupt cache")
        with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
            self.run_download([(0, b"wrong downloaded contents")])
        self.assertEqual(self.destination.read_bytes(), b"old corrupt cache")
        self.assertEqual(len(self.calls), 1)
        self.sleeps.assert_not_called()
        self.assert_no_partial_files()

    def test_certificate_and_http_errors_fail_without_retry(self):
        for code in (22, 60):
            with self.subTest(code=code), self.assertRaisesRegex(RuntimeError, f"curl exit {code}"):
                self.run_download([(code, b"unverified")])
            self.assertFalse(self.destination.exists())
            self.assert_no_partial_files()
        self.assertEqual(len(self.calls), 2)
        self.sleeps.assert_not_called()

    def test_cached_symlink_is_not_trusted(self):
        original = self.directory / "original"
        original.write_bytes(self.body)
        self.destination.symlink_to(original)
        self.run_download([(0, self.body)])
        self.assertFalse(self.destination.is_symlink())
        self.assertEqual(original.read_bytes(), self.body)
        self.assertEqual(len(self.calls), 1)
        self.assert_no_partial_files()

    def test_deadline_bounds_attempts_and_cleanup(self):
        with self.assertRaisesRegex(RuntimeError, "deadline exceeded"):
            self.run_download([("timeout", b"partial", 95), ("timeout", b"partial", 84)], deadline=180)
        self.assertEqual(self.timeouts, [95, 84])
        self.assertEqual(self.clock, 180)
        self.assertFalse(self.destination.exists())
        self.assert_no_partial_files()

    def test_expired_deadline_starts_no_download(self):
        with self.assertRaisesRegex(RuntimeError, "deadline exceeded"):
            self.run_download([], deadline=0)
        self.assertEqual(self.calls, [])
        self.assert_no_partial_files()

    def test_all_archives_share_one_deadline(self):
        with patch.object(download_shells.sys, "argv", ["download_minimum_shells.py", str(self.directory)]):
            with patch.object(download_shells, "download", side_effect=lambda *args: self.advance_clock(30)) as download:
                download_shells.main()
        self.assertEqual([call.args for call in download.call_args_list],
                         [(self.directory, *archive, 180) for archive in download_shells.ARCHIVES])


if __name__ == "__main__":
    unittest.main()
