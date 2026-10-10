#!/usr/bin/env python3
"""Check cleanup ordering without creating processes, signals, or terminals."""

from contextlib import ExitStack
import hashlib
import importlib.util
import io
from pathlib import Path
import signal
import subprocess
import unittest
import unittest.mock as mock

spec = importlib.util.spec_from_file_location("completion_native_driver", Path(__file__).with_name("native_check.py"))
driver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(driver)


class NativeCleanupTests(unittest.TestCase):
    def setUp(self):
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.events = []
        self.reaped = False
        self.pid, self.terminal = 41001, 41002
        self.transcript = io.BytesIO()
        self.addCleanup(self.transcript.close)
        self.patch(driver.pty, "fork", return_value=(self.pid, self.terminal))
        self.patch(driver.fcntl, "ioctl")
        self.patch(driver.os, "getpgrp", return_value=41000)
        self.foreground = self.patch(driver.os, "tcgetpgrp", return_value=self.pid)
        self.session_id = self.patch(driver.os, "getsid", return_value=self.pid)
        self.patch(driver.os, "getpgid", side_effect=lambda group: group)
        self.kill = self.patch(driver.os, "killpg", side_effect=self.signal_group)
        self.wait = self.patch(driver.os, "waitpid", side_effect=self.reap)
        self.close_fd = self.patch(driver.os, "close")
        self.read = self.patch(driver.os, "read", return_value=b"")
        self.patch(driver.select, "select", return_value=([self.terminal], [], []))
        self.patch(driver.time, "sleep")
        self.ticks = iter(index / 10 for index in range(1000))
        self.patch(driver.time, "monotonic", side_effect=lambda: next(self.ticks))
        self.popen = self.patch(driver.subprocess, "Popen", side_effect=AssertionError("unexpected process creation"))
        transcript_path = mock.Mock()
        transcript_path.open.return_value = self.transcript
        self.session = driver.ShellSession("bash", "synthetic-shell", {}, ".", transcript_path)

    def patch(self, owner, name, **options):
        return self.stack.enter_context(mock.patch.object(owner, name, **options))

    def signal_group(self, group, number):
        self.assertFalse(self.reaped, "cleanup signaled a numeric group after reaping its ownership anchor")
        self.events.append(("signal", group, number))

    def reap(self, pid, _options):
        self.events.append(("reap", pid))
        self.reaped = True
        return pid, 7 << 8

    def assert_closed(self):
        self.close_fd.assert_called_once_with(self.terminal)
        self.assertTrue(self.transcript.closed)

    def test_pump_does_not_reap_session_leader(self):
        self.session.pump()
        self.assertTrue(self.session.eof)
        self.assertIsNone(self.session.status)
        self.wait.assert_not_called()

    def test_wait_for_reports_eof_without_reaping(self):
        with self.assertRaisesRegex(RuntimeError, "EOF"):
            self.session.wait_for(lambda: False)
        self.wait.assert_not_called()

    def test_each_signal_phase_runs_once_before_reaping(self):
        self.session.close()
        # The foreground and shell share this group. Duplicate KILL is unsafe.
        self.assertEqual(self.kill.call_args_list, [
            mock.call(self.pid, signal.SIGTERM), mock.call(self.pid, signal.SIGKILL),
        ])
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assertEqual(self.session.status, 7)
        self.assert_closed()

    def test_already_reaped_child_is_not_signaled(self):
        self.session.status = 17
        self.reaped = True
        self.session.close()
        self.kill.assert_not_called()
        self.wait.assert_not_called()
        self.foreground.assert_not_called()
        self.assertEqual(self.session.status, 17)
        self.assert_closed()

    def test_uncertain_foreground_does_not_block_owned_cleanup(self):
        self.foreground.return_value = 42001
        self.session_id.return_value = 43001
        with self.assertRaisesRegex(RuntimeError, "ownership is uncertain"):
            self.session.close()
        self.assertEqual(self.kill.call_args_list, [
            mock.call(self.pid, signal.SIGTERM), mock.call(self.pid, signal.SIGKILL),
        ])
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assert_closed()

    def test_denied_term_remains_visible_after_kill_and_reap(self):
        denied = PermissionError("synthetic TERM denial")

        def deny_term(group, number):
            self.signal_group(group, number)
            if number == signal.SIGTERM:
                raise denied

        self.kill.side_effect = deny_term
        with self.assertRaises(PermissionError) as caught:
            self.session.close()
        self.assertIs(caught.exception, denied)
        self.assertIn(("signal", self.pid, signal.SIGKILL), self.events)
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assert_closed()

    def test_pump_failure_preserves_capture_error_and_cleanup(self):
        self.read.side_effect = OSError("synthetic transcript read failure")
        try:
            raise TimeoutError("original capture failure")
        except TimeoutError:
            with self.assertRaisesRegex(RuntimeError, "original capture failure.*synthetic transcript read failure"):
                self.session.close()
        self.assertIn(("signal", self.pid, signal.SIGKILL), self.events)
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assert_closed()

    def test_descriptor_failure_still_closes_transcript(self):
        self.session.status = 0
        denied = OSError("synthetic descriptor close failure")
        self.close_fd.side_effect = denied
        with self.assertRaises(OSError) as caught:
            self.session.close()
        self.assertIs(caught.exception, denied)
        self.assertTrue(self.transcript.closed)
        self.kill.assert_not_called()

    def check_close_cancellation(self, control):
        cleanup_error = OSError("synthetic descriptor cleanup failure")
        self.close_fd.side_effect = cleanup_error
        stderr = io.StringIO()
        with mock.patch.object(driver.sys, "stderr", stderr):
            try:
                raise control
            except BaseException:
                with self.assertRaises(type(control)) as caught:
                    self.session.close()
        self.assertIs(caught.exception, control)
        self.assertIn(cleanup_error, control.cleanup_errors)
        self.assertIn("synthetic descriptor cleanup failure", stderr.getvalue())
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assert_closed()

    def test_close_preserves_system_exit_with_cleanup_failure(self):
        control = SystemExit(143)
        self.check_close_cancellation(control)
        self.assertEqual(control.code, 143)

    def test_close_preserves_keyboard_interrupt_with_cleanup_failure(self):
        self.check_close_cancellation(KeyboardInterrupt())

    def check_interrupted_reap(self, control):
        attempts = 0

        def interrupted_once(pid, options):
            nonlocal attempts
            attempts += 1
            if attempts == 1:
                raise control
            return self.reap(pid, options)

        self.wait.side_effect = interrupted_once
        with self.assertRaises(type(control)) as caught:
            self.session.close()
        self.assertIs(caught.exception, control)
        self.assertEqual(attempts, 2)
        self.assertEqual(self.session.status, 7)
        self.assertEqual(self.events[-1], ("reap", self.pid))
        self.assert_closed()

    def test_reap_retries_system_exit_then_preserves_status(self):
        control = SystemExit(143)
        self.check_interrupted_reap(control)
        self.assertEqual(control.code, 143)

    def test_reap_retries_keyboard_interrupt(self):
        self.check_interrupted_reap(KeyboardInterrupt())

    def test_diagnostic_write_failure_does_not_replace_cancellation(self):
        control = SystemExit(143)
        self.close_fd.side_effect = OSError("synthetic descriptor cleanup failure")
        stderr = mock.Mock()
        denied = OSError("synthetic diagnostic failure")
        stderr.write.side_effect = denied
        with mock.patch.object(driver.sys, "stderr", stderr):
            try:
                raise control
            except BaseException:
                with self.assertRaises(SystemExit) as caught:
                    self.session.close()
        self.assertIs(caught.exception, control)
        self.assertEqual(control.code, 143)
        self.assertIn(denied, control.cleanup_errors)
        self.assert_closed()

    def test_adapter_success_does_not_signal_reaped_child(self):
        process = mock.Mock(pid=self.pid, returncode=None)

        def communicate(**_kwargs):
            process.returncode = 0
            self.reaped = True
            return b"synthetic adapter\n", b""

        process.communicate.side_effect = communicate
        self.popen.side_effect = None
        self.popen.return_value = process
        destination = mock.Mock()
        digest = driver.generate_adapter("synthetic-openai", "bash", {}, destination)
        self.assertEqual(digest, hashlib.sha256(b"synthetic adapter\n").hexdigest())
        destination.write_bytes.assert_called_once_with(b"synthetic adapter\n")
        destination.with_suffix.return_value.write_bytes.assert_called_once_with(b"")
        self.kill.assert_not_called()

    def test_adapter_timeout_signals_only_an_unreaped_child(self):
        for already_reaped in (False, True):
            with self.subTest(already_reaped=already_reaped):
                self.reaped = already_reaped
                self.kill.reset_mock()
                process = mock.Mock(pid=self.pid, returncode=0 if already_reaped else None)
                original = subprocess.TimeoutExpired("synthetic-openai", 1, output=b"partial adapter")
                process.communicate.side_effect = [original, (b"partial adapter", b"")]
                self.popen.side_effect = None
                self.popen.return_value = process
                with self.assertRaises(subprocess.TimeoutExpired) as caught:
                    driver.generate_adapter("synthetic-openai", "bash", {}, mock.Mock())
                self.assertIs(caught.exception, original)
                self.assertEqual(caught.exception.output, b"partial adapter")
                if already_reaped:
                    self.kill.assert_not_called()
                else:
                    self.kill.assert_called_once_with(self.pid, signal.SIGKILL)

    def check_adapter_cancellation(self, control):
        process = mock.Mock(pid=self.pid, returncode=None)
        calls = 0

        def communicate(**_kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                raise control
            process.returncode = 0
            self.reaped = True
            process.stdout.close()
            process.stderr.close()
            return b"synthetic adapter", b""

        process.communicate.side_effect = communicate
        self.popen.side_effect = None
        self.popen.return_value = process
        denied = PermissionError("synthetic adapter cleanup denial")

        def deny_kill(group, number):
            self.signal_group(group, number)
            raise denied

        self.kill.side_effect = deny_kill
        stderr = io.StringIO()
        with mock.patch.object(driver.sys, "stderr", stderr):
            with self.assertRaises(type(control)) as caught:
                driver.generate_adapter("synthetic-openai", "bash", {}, mock.Mock())
        self.assertIs(caught.exception, control)
        self.assertIn(denied, control.cleanup_errors)
        self.assertIn("synthetic adapter cleanup denial", stderr.getvalue())
        self.assertTrue(self.reaped)
        process.stdout.close.assert_called_once()
        process.stderr.close.assert_called_once()
        self.kill.assert_called_once_with(self.pid, signal.SIGKILL)

    def test_adapter_preserves_system_exit_with_cleanup_failure(self):
        control = SystemExit(143)
        self.check_adapter_cancellation(control)
        self.assertEqual(control.code, 143)

    def test_adapter_preserves_keyboard_interrupt_with_cleanup_failure(self):
        self.check_adapter_cancellation(KeyboardInterrupt())

    def test_adapter_retries_interrupted_cleanup_without_more_signals(self):
        original = SystemExit(143)
        interruption = KeyboardInterrupt()
        process = mock.Mock(pid=self.pid, returncode=None)
        calls = 0

        def communicate(**_kwargs):
            nonlocal calls
            calls += 1
            if calls == 1:
                raise original
            if calls == 2:
                raise interruption
            process.returncode = 0
            self.reaped = True
            return b"synthetic adapter", b""

        process.communicate.side_effect = communicate
        self.popen.side_effect = None
        self.popen.return_value = process
        with mock.patch.object(driver.sys, "stderr", io.StringIO()):
            with self.assertRaises(SystemExit) as caught:
                driver.generate_adapter("synthetic-openai", "bash", {}, mock.Mock())
        self.assertIs(caught.exception, original)
        self.assertEqual(original.code, 143)
        self.assertIn(interruption, original.cleanup_errors)
        self.assertEqual(calls, 3)
        self.assertTrue(self.reaped)
        self.kill.assert_called_once_with(self.pid, signal.SIGKILL)


if __name__ == "__main__":
    unittest.main(verbosity=2)
