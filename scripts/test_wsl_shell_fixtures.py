#!/usr/bin/env python3
"""Offline controls for bounded fixture captures and owned PTY cleanup."""
from contextlib import ExitStack
import errno
import importlib.util
import os
from pathlib import Path
import signal
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("fixture_harness", HERE / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
startup_spec = importlib.util.spec_from_file_location("startup_fixture", HERE / "check-wsl-shell-startup.py")
startup = importlib.util.module_from_spec(startup_spec)
startup_spec.loader.exec_module(startup)


class Pipe:
    def __init__(self, descriptor):
        self.descriptor = descriptor
        self.closed = False

    def fileno(self):
        return self.descriptor

    def close(self):
        self.closed = True


class Capture:
    """Supply fake process and pipe events; never launch a command or send a signal."""
    def __init__(self, stdout, stderr, code=0, read_error=None, wait_error=None):
        self.clock = 0.0
        self.queues = {10: list(stdout), 11: list(stderr)}
        self.keys = {}
        self.signals = []
        self.waits = []
        self.read_error = read_error
        self.wait_error = wait_error
        self.process = SimpleNamespace(pid=321, stdout=Pipe(10), stderr=Pipe(11), returncode=code)
        self.process.poll = lambda: self.process.returncode
        self.process.wait = self.wait
        self.selector = SimpleNamespace(register=self.register, unregister=self.unregister,
                                        select=self.select, close=lambda: None)

    def register(self, pipe, _events, name):
        self.keys[pipe.fileno()] = SimpleNamespace(fd=pipe.fileno(), fileobj=pipe, data=name)

    def unregister(self, pipe):
        self.keys.pop(pipe.fileno())

    def select(self, seconds):
        self.clock += seconds
        return [(key, 1) for descriptor, key in list(self.keys.items()) if self.queues[descriptor]]

    def read(self, descriptor, count):
        if self.read_error is not None:
            raise self.read_error
        data = self.queues[descriptor].pop(0)
        if len(data) > count:
            self.queues[descriptor].insert(0, data[count:])
        return data[:count]

    def kill(self, pid, selected_signal):
        self.signals.append((pid, selected_signal))
        self.process.returncode = -9

    def wait(self, timeout):
        self.waits.append(timeout)
        if self.wait_error is not None:
            raise self.wait_error
        if self.process.returncode is None:
            raise subprocess.TimeoutExpired("fake", timeout)
        return self.process.returncode

    def run(self, **kwargs):
        with ExitStack() as stack:
            stack.enter_context(mock.patch.object(harness.subprocess, "Popen", return_value=self.process))
            stack.enter_context(mock.patch.object(harness.selectors, "DefaultSelector", return_value=self.selector))
            stack.enter_context(mock.patch.object(harness.os, "set_blocking"))
            stack.enter_context(mock.patch.object(harness.os, "read", side_effect=self.read))
            stack.enter_context(mock.patch.object(harness.os, "killpg", side_effect=self.kill))
            stack.enter_context(mock.patch.object(harness.time, "monotonic", side_effect=lambda: self.clock))
            return harness.capture_fixture(["never-executed"], env={}, timeout=0.2, **kwargs)


class CaptureTests(unittest.TestCase):
    def test_success_requires_two_pipe_eofs(self):
        fake = Capture([b"out", b""], [b"err", b""])
        got = fake.run()
        self.assertEqual((got.returncode, got.stdout, got.stderr), (0, b"out", b"err"))
        self.assertTrue(got.stdout_eof and got.stderr_eof)
        self.assertEqual(fake.signals, [])
        self.assertTrue(fake.process.stdout.closed and fake.process.stderr.closed)

    def test_combined_exact_byte_limit_passes(self):
        fake = Capture([b"ab", b""], [b"cd", b""])
        got = fake.run(limit=4)
        self.assertEqual(got.stdout + got.stderr, b"abcd")

    def test_combined_byte_overflow_kills_and_joins_owned_child(self):
        fake = Capture([b"abc"], [b"de"], code=None)
        with self.assertRaisesRegex(RuntimeError, "bound"):
            fake.run(limit=4)
        self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
        self.assertIn(2, fake.waits)
        self.assertTrue(fake.process.stdout.closed and fake.process.stderr.closed)

    def test_stdout_overflow_cannot_hide_behind_empty_stderr(self):
        fake = Capture([b"abcde"], [b""], code=None)
        with self.assertRaisesRegex(RuntimeError, "bound"):
            fake.run(limit=4)
        self.assertEqual(fake.process.returncode, -9)

    def test_stderr_overflow_is_rejected(self):
        fake = Capture([b""], [b"abcde"], code=None)
        with self.assertRaisesRegex(RuntimeError, "bound"):
            fake.run(limit=4)

    def test_nonzero_exit_is_preserved(self):
        fake = Capture([b"partial", b""], [b"failure", b""], code=7)
        got = fake.run()
        self.assertEqual(got.returncode, 7)
        self.assertEqual(fake.signals, [])

    def test_exit_without_stderr_eof_is_not_success(self):
        fake = Capture([b""], [])
        with self.assertRaisesRegex(TimeoutError, "pipe drain"):
            fake.run()
        self.assertTrue(fake.process.fixture_stdout_eof)
        self.assertFalse(fake.process.fixture_stderr_eof)
        self.assertEqual(fake.signals, [])

    def test_live_child_timeout_is_killed_and_joined(self):
        fake = Capture([], [], code=None)
        with self.assertRaises(TimeoutError):
            fake.run()
        self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
        self.assertEqual(fake.process.returncode, -9)

    def test_exited_unreaped_leader_with_open_pipe_keeps_group_ownership(self):
        fake = Capture([b""], [], code=None)
        fake.process.poll = mock.Mock(side_effect=AssertionError("reaped before pipe EOF"))

        def wait(timeout):
            fake.waits.append(timeout)
            fake.process.returncode = 0
            return 0

        fake.process.wait = wait
        with self.assertRaises(TimeoutError):
            fake.run()
        self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
        self.assertEqual(fake.waits, [2])
        fake.process.poll.assert_not_called()

    def test_read_error_keeps_cleanup(self):
        fake = Capture([b"x"], [], code=None, read_error=OSError("synthetic read failure"))
        with self.assertRaisesRegex(OSError, "synthetic read failure"):
            fake.run()
        self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
        self.assertTrue(fake.process.stdout.closed and fake.process.stderr.closed)

    def test_failed_join_cannot_report_success(self):
        fake = Capture([b""], [b""], wait_error=subprocess.TimeoutExpired("fake", 2))
        with self.assertRaises(subprocess.TimeoutExpired):
            fake.run()

    def test_owner_registry_keeps_process_object(self):
        fake = Capture([b""], [b""])
        owned = []
        fake.run(owned_processes=owned)
        self.assertEqual(owned, [fake.process])

    def test_invalid_bounds_fail_before_launch(self):
        with mock.patch.object(harness.subprocess, "Popen") as launch:
            for kwargs in ({"limit": 0}, {"limit": -1}, {"timeout": 0}):
                with self.subTest(kwargs=kwargs), self.assertRaises(ValueError):
                    harness.capture_fixture(["never-executed"], env={}, **kwargs)
            launch.assert_not_called()

    def test_selector_constructor_failure_cannot_launch_child(self):
        with mock.patch.object(harness.subprocess, "Popen") as launch, \
                mock.patch.object(harness.selectors, "DefaultSelector", side_effect=OSError("selector failure")):
            with self.assertRaisesRegex(OSError, "selector failure"):
                harness.capture_fixture(["never-executed"], env={})
            launch.assert_not_called()

    def test_launch_failure_closes_selector(self):
        selector = mock.Mock()
        with mock.patch.object(harness.subprocess, "Popen", side_effect=OSError("launch failure")), \
                mock.patch.object(harness.selectors, "DefaultSelector", return_value=selector):
            with self.assertRaisesRegex(OSError, "launch failure"):
                harness.capture_fixture(["never-executed"], env={})
        selector.close.assert_called_once_with()


class TerminalTests(unittest.TestCase):
    def terminal(self, raw_limit=4, event_limit=2, event_bytes=4):
        terminal = harness.Terminal.__new__(harness.Terminal)
        terminal.master, terminal.slave = 20, 21
        terminal.raw, terminal.events = bytearray(), []
        terminal.max_output_bytes = raw_limit
        terminal.max_events = event_limit
        terminal.max_event_bytes = event_bytes
        terminal.event_bytes = 0
        terminal.started = 0
        terminal.deadline = None
        terminal.eof = False
        terminal._slave_closed = terminal._master_closed = False
        terminal.retain_child_identity = False
        terminal._identity_lost = False
        terminal.child = mock.Mock(pid=456)
        terminal.child.returncode = 0
        terminal.child.poll.return_value = 0
        return terminal

    def read(self, terminal, data=None, error=None):
        with mock.patch.object(harness.select, "select", return_value=([20], [], [])), \
                mock.patch.object(harness.os, "read", return_value=data, side_effect=error):
            terminal.read()

    def test_raw_exact_limit_is_accepted(self):
        terminal = self.terminal()
        self.read(terminal, b"abcd")
        self.assertEqual(bytes(terminal.raw), b"abcd")
        self.assertEqual(len(terminal.events), 1)

    def test_raw_overflow_is_rejected_before_retention(self):
        terminal = self.terminal()
        self.read(terminal, b"abcd")
        with self.assertRaisesRegex(RuntimeError, "output"):
            self.read(terminal, b"e")
        self.assertEqual(bytes(terminal.raw), b"abcd")
        self.assertEqual(len(terminal.events), 1)

    def test_event_count_overflow_cannot_grow_raw(self):
        terminal = self.terminal(raw_limit=100, event_limit=1, event_bytes=100)
        self.read(terminal, b"a")
        with self.assertRaisesRegex(RuntimeError, "event count"):
            self.read(terminal, b"b")
        self.assertEqual(bytes(terminal.raw), b"a")
        self.assertEqual(len(terminal.events), 1)

    def test_malformed_utf8_counts_replacement_event_bytes(self):
        terminal = self.terminal(raw_limit=100, event_bytes=2)
        with self.assertRaisesRegex(RuntimeError, "event bytes"):
            self.read(terminal, b"\xff")
        self.assertEqual(terminal.raw, b"")
        self.assertEqual(terminal.events, [])

    def test_eof_does_not_append_empty_events(self):
        terminal = self.terminal()
        self.read(terminal, b"")
        self.assertTrue(terminal.eof)
        self.assertEqual(terminal.events, [])

    def test_eio_marks_pty_eof(self):
        terminal = self.terminal()
        self.read(terminal, error=OSError(errno.EIO, "closed PTY"))
        self.assertTrue(terminal.eof)

    def test_deadline_rejects_before_read(self):
        terminal = self.terminal()
        terminal.deadline = 1
        with mock.patch.object(harness.time, "monotonic", return_value=2), \
                mock.patch.object(harness.os, "read") as read:
            with self.assertRaises(TimeoutError):
                terminal.read()
            read.assert_not_called()

    def test_completed_child_close_sends_no_signal(self):
        terminal = self.terminal()
        with mock.patch.object(harness.os, "close") as close, mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_not_called()
            self.assertEqual(close.call_args_list, [mock.call(20), mock.call(21)])
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_live_child_close_signals_owned_group_and_joins(self):
        terminal = self.terminal()
        terminal.child.poll.return_value = None
        with mock.patch.object(harness.os, "close"), mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_called_once_with(456, signal.SIGKILL)
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_retained_exit_observation_does_not_reap(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal.child.returncode = None
        with mock.patch.object(harness.os, "waitid", return_value=SimpleNamespace(si_pid=456), create=True), \
                mock.patch.object(harness.os, "WNOWAIT", 0x1000000, create=True), \
                mock.patch.object(harness.os, "P_PID", 1, create=True), \
                mock.patch.object(harness.os, "WEXITED", 4, create=True), \
                mock.patch.object(harness.os, "WNOHANG", 1, create=True):
            self.assertFalse(terminal.running())
        terminal.child.poll.assert_not_called()
        self.assertIsNone(terminal.child.returncode)

    def test_retained_exited_leader_cleanup_signals_before_join(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal.child.returncode = None
        with mock.patch.object(harness.os, "close"), mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_called_once_with(456, signal.SIGKILL)
            terminal.child.poll.assert_not_called()
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_lost_child_identity_prevents_group_signal(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal._identity_lost = True
        terminal.child.returncode = None
        with mock.patch.object(harness.os, "close"), mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_not_called()

    def test_drain_rejects_live_child(self):
        terminal = self.terminal()
        terminal.child.poll.return_value = None
        with mock.patch.object(harness.os, "close") as close:
            with self.assertRaisesRegex(RuntimeError, "before child exit"):
                terminal.drain_eof()
            close.assert_not_called()

    def test_drain_closes_parent_slave_before_eof(self):
        terminal = self.terminal()
        with mock.patch.object(harness.os, "close") as close, \
                mock.patch.object(harness.select, "select", return_value=([20], [], [])), \
                mock.patch.object(harness.os, "read", side_effect=OSError(errno.EIO, "closed PTY")):
            terminal.drain_eof()
            close.assert_called_once_with(21)
            self.assertTrue(terminal.eof and terminal._slave_closed)


class FixtureFileTests(unittest.TestCase):
    def test_small_file_preserves_bytes(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "receipt"
            path.write_bytes(b"a\x00b\n")
            self.assertEqual(harness.read_fixture_file(path, 4), b"a\x00b\n")

    def test_oversized_file_rejects_before_complete_read(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "receipt"
            path.write_bytes(b"abcde")
            with self.assertRaisesRegex(RuntimeError, "bound"):
                harness.read_fixture_file(path, 4)

    def test_symlink_is_not_a_regular_owned_receipt(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "receipt"
            target = Path(root) / "target"
            target.write_bytes(b"data")
            path.symlink_to(target)
            with self.assertRaises(OSError):
                harness.read_fixture_file(path)

    def test_fifo_is_rejected_without_blocking(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "receipt"
            os.mkfifo(path)
            with self.assertRaisesRegex(RuntimeError, "regular"):
                harness.read_fixture_file(path)


class SystemStartupSourceTests(unittest.TestCase):
    def test_unchanged_source_retains_complete_bytes_and_identity(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profile"
            path.write_bytes(b"# synthetic startup\nexport EXAMPLE=value\n")
            before = startup.system_source(path)
            after, unchanged = startup.recheck_system_sources([before])
            self.assertTrue(unchanged)
            self.assertEqual(after, [before])
            self.assertTrue(before["present"])
            self.assertEqual(before["bytes"], path.stat().st_size)
            self.assertEqual(startup.base64.b64decode(before["content_base64"]), path.read_bytes())
            self.assertEqual(before["sha256"], startup.hashlib.sha256(path.read_bytes()).hexdigest())
            self.assertNotIn("atime_ns", before["identity"])

    def test_replaced_source_fails_even_with_identical_bytes(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profile"
            path.write_bytes(b"# same contents\n")
            before = startup.system_source(path)
            replacement = Path(root) / "replacement"
            replacement.write_bytes(path.read_bytes())
            os.replace(replacement, path)
            after, unchanged = startup.recheck_system_sources([before])
            self.assertFalse(unchanged)
            self.assertEqual(after[0]["sha256"], before["sha256"])
            self.assertNotEqual(after[0]["identity"]["inode"], before["identity"]["inode"])

    def test_missing_source_is_explicit_and_presence_changes_fail(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "bash.bashrc"
            absent = startup.system_source(path)
            self.assertEqual(absent, {"path": str(path), "present": False})
            self.assertTrue(startup.recheck_system_sources([absent])[1])
            path.write_bytes(b"# appeared\n")
            appeared, unchanged = startup.recheck_system_sources([absent])
            self.assertFalse(unchanged)
            self.assertTrue(appeared[0]["present"])
            path.unlink()
            removed, unchanged = startup.recheck_system_sources(appeared)
            self.assertFalse(unchanged)
            self.assertFalse(removed[0]["present"])

    def test_oversized_source_fails_before_open(self):
        with tempfile.TemporaryDirectory() as root:
            path = Path(root) / "profile"
            path.write_bytes(b"x" * (startup.SMALL + 1))
            with mock.patch.object(startup.os, "open") as opened:
                with self.assertRaisesRegex(RuntimeError, "exceeds64KiB"):
                    startup.system_source(path)
                opened.assert_not_called()


if __name__ == "__main__":
    unittest.main()
