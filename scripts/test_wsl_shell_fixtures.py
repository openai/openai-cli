#!/usr/bin/env python3
"""Offline controls for bounded fixture captures and owned PTY cleanup."""
from contextlib import ExitStack, redirect_stderr, redirect_stdout
from copy import deepcopy
import errno
import importlib.util
import io
import json
import os
from pathlib import Path
import signal
import struct
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
import unittest.mock


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("fixture_harness", HERE / "image_picker_harness.py")
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)
startup_spec = importlib.util.spec_from_file_location("startup_fixture", HERE / "check-wsl-shell-startup.py")
startup = importlib.util.module_from_spec(startup_spec)
startup_spec.loader.exec_module(startup)
models_spec = importlib.util.spec_from_file_location("models_fixture", HERE / "check-wsl-models.py")
models = importlib.util.module_from_spec(models_spec)
models_spec.loader.exec_module(models)
pilot_spec = importlib.util.spec_from_file_location("fixture_controller", HERE / "check-wsl-pager.py")
pilot = importlib.util.module_from_spec(pilot_spec)
pilot_spec.loader.exec_module(pilot)


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
            stack.enter_context(unittest.mock.patch.object(harness.subprocess, "Popen", return_value=self.process))
            stack.enter_context(unittest.mock.patch.object(harness.selectors, "DefaultSelector", return_value=self.selector))
            stack.enter_context(unittest.mock.patch.object(harness.os, "set_blocking"))
            stack.enter_context(unittest.mock.patch.object(harness.os, "read", side_effect=self.read))
            stack.enter_context(unittest.mock.patch.object(harness.os, "killpg", side_effect=self.kill))
            stack.enter_context(unittest.mock.patch.object(harness.time, "monotonic", side_effect=lambda: self.clock))
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
        fake.process.poll = unittest.mock.Mock(side_effect=AssertionError("reaped before pipe EOF"))

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
        for error in (OSError("synthetic read failure"), KeyboardInterrupt(), SystemExit(7)):
            with self.subTest(error=type(error).__name__):
                fake = Capture([b"x"], [], code=None, read_error=error)
                with self.assertRaises(type(error)) as raised:
                    fake.run()
                self.assertIs(raised.exception, error)
                self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
                self.assertTrue(fake.process.stdout.closed and fake.process.stderr.closed)

    def test_read_interrupt_survives_ordinary_cleanup_failures(self):
        for error in (KeyboardInterrupt("capture interrupted"), SystemExit(7), GeneratorExit("capture closed")):
            with self.subTest(error=type(error).__name__):
                fake = Capture([b"x"], [], code=None, read_error=error)
                first, second = OSError("selector close"), OSError("stdout close")
                fake.selector.close = unittest.mock.Mock(side_effect=first)
                fake.process.stdout.close = unittest.mock.Mock(side_effect=second)
                with self.assertRaises(type(error)) as caught:
                    fake.run()
                self.assertIs(caught.exception, error)
                self.assertEqual(error.fixture_cleanup_errors, (first, second))
                self.assertIs(error.__cause__, second)
                self.assertTrue(fake.process.stderr.closed)
                self.assertEqual(fake.signals, [(321, signal.SIGKILL)])
                self.assertEqual(fake.waits, [2])
                self.assertFalse(fake.process.fixture_cleanup_complete)

    def test_cleanup_interrupts_attempt_later_pipe_and_child_cleanup(self):
        for phase in ("selector", "stdout", "stderr", "kill", "wait"):
            for error in (KeyboardInterrupt("cleanup interrupted"), SystemExit(7)):
                with self.subTest(phase=phase, error=type(error).__name__):
                    fake = Capture([b"x"], [], code=None, read_error=OSError("read failed"))
                    if phase == "selector":
                        fake.selector.close = unittest.mock.Mock(side_effect=error)
                    elif phase in ("stdout", "stderr"):
                        getattr(fake.process, phase).close = unittest.mock.Mock(side_effect=error)
                    elif phase == "kill":
                        fake.kill = unittest.mock.Mock(side_effect=error)
                    else:
                        fake.wait_error = error
                    with self.assertRaises(type(error)) as caught:
                        fake.run()
                    self.assertIs(caught.exception, error)
                    if phase != "stdout":
                        self.assertTrue(fake.process.stdout.closed)
                    if phase != "stderr":
                        self.assertTrue(fake.process.stderr.closed)
                    self.assertEqual(fake.waits, [2])
                    self.assertFalse(fake.process.fixture_cleanup_complete)

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
        with unittest.mock.patch.object(harness.subprocess, "Popen") as launch:
            for kwargs in ({"limit": 0}, {"limit": -1}, {"timeout": 0}):
                with self.subTest(kwargs=kwargs), self.assertRaises(ValueError):
                    harness.capture_fixture(["never-executed"], env={}, **kwargs)
            launch.assert_not_called()

    def test_selector_constructor_failure_cannot_launch_child(self):
        with unittest.mock.patch.object(harness.subprocess, "Popen") as launch, \
                unittest.mock.patch.object(harness.selectors, "DefaultSelector", side_effect=OSError("selector failure")):
            with self.assertRaisesRegex(OSError, "selector failure"):
                harness.capture_fixture(["never-executed"], env={})
            launch.assert_not_called()

    def test_launch_failure_closes_selector(self):
        selector = unittest.mock.Mock()
        with unittest.mock.patch.object(harness.subprocess, "Popen", side_effect=OSError("launch failure")), \
                unittest.mock.patch.object(harness.selectors, "DefaultSelector", return_value=selector):
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
        terminal.child = unittest.mock.Mock(pid=456)
        terminal.child.returncode = 0
        terminal.child.poll.return_value = 0
        return terminal

    def read(self, terminal, data=None, error=None):
        with unittest.mock.patch.object(harness.select, "select", return_value=([20], [], [])), \
                unittest.mock.patch.object(harness.os, "read", return_value=data, side_effect=error):
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
        with unittest.mock.patch.object(harness.time, "monotonic", return_value=2), \
                unittest.mock.patch.object(harness.os, "read") as read:
            with self.assertRaises(TimeoutError):
                terminal.read()
            read.assert_not_called()

    def test_completed_child_close_sends_no_signal(self):
        terminal = self.terminal()
        with unittest.mock.patch.object(harness.os, "close") as close, unittest.mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_not_called()
            self.assertEqual(close.call_args_list, [unittest.mock.call(20), unittest.mock.call(21)])
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_live_child_close_signals_owned_group_and_joins(self):
        terminal = self.terminal()
        terminal.child.poll.return_value = None
        with unittest.mock.patch.object(harness.os, "close"), unittest.mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_called_once_with(456, signal.SIGKILL)
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_close_interrupt_attempts_both_pty_handles_and_child_wait(self):
        for phase in ("kill", "master", "slave"):
            for error in (KeyboardInterrupt("PTY interrupted"), SystemExit(7)):
                with self.subTest(phase=phase, error=type(error).__name__):
                    terminal = self.terminal()
                    terminal.child.poll.return_value = None

                    def close(descriptor):
                        if descriptor == (20 if phase == "master" else 21 if phase == "slave" else -1):
                            raise error

                    with unittest.mock.patch.object(harness.os, "close", side_effect=close) as handles, \
                            unittest.mock.patch.object(harness.os, "killpg", side_effect=error if phase == "kill" else None):
                        with self.assertRaises(type(error)) as caught:
                            terminal.close()
                    self.assertIs(caught.exception, error)
                    self.assertEqual(handles.call_args_list, [unittest.mock.call(20), unittest.mock.call(21)])
                    terminal.child.wait.assert_called_once_with(timeout=5)
                    self.assertEqual(terminal._master_closed, phase != "master")
                    self.assertEqual(terminal._slave_closed, phase != "slave")

    def test_close_failure_preserves_pending_interrupt_and_later_cleanup(self):
        for error in (KeyboardInterrupt("PTY execution interrupted"), SystemExit(7)):
            with self.subTest(error=type(error).__name__):
                terminal = self.terminal()
                failure = OSError("master close failed")

                def close(descriptor):
                    if descriptor == 20:
                        raise failure

                def trigger():
                    try:
                        raise error
                    finally:
                        terminal.close()

                with unittest.mock.patch.object(harness.os, "close", side_effect=close) as handles:
                    with self.assertRaises(type(error)) as caught:
                        trigger()
                self.assertIs(caught.exception, error)
                self.assertIs(error.__cause__, failure)
                self.assertEqual(error.fixture_cleanup_errors, (failure,))
                self.assertEqual(handles.call_args_list, [unittest.mock.call(20), unittest.mock.call(21)])
                terminal.child.wait.assert_called_once_with(timeout=5)
                self.assertFalse(terminal._master_closed)
                self.assertTrue(terminal._slave_closed)

    def test_retained_exit_observation_does_not_reap(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal.child.returncode = None
        with unittest.mock.patch.object(harness.os, "waitid", return_value=SimpleNamespace(si_pid=456), create=True), \
                unittest.mock.patch.object(harness.os, "WNOWAIT", 0x1000000, create=True), \
                unittest.mock.patch.object(harness.os, "P_PID", 1, create=True), \
                unittest.mock.patch.object(harness.os, "WEXITED", 4, create=True), \
                unittest.mock.patch.object(harness.os, "WNOHANG", 1, create=True):
            self.assertFalse(terminal.running())
        terminal.child.poll.assert_not_called()
        self.assertIsNone(terminal.child.returncode)

    def test_retained_exited_leader_cleanup_signals_before_join(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal.child.returncode = None
        with unittest.mock.patch.object(harness.os, "close"), unittest.mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_called_once_with(456, signal.SIGKILL)
            terminal.child.poll.assert_not_called()
            terminal.child.wait.assert_called_once_with(timeout=5)

    def test_lost_child_identity_prevents_group_signal(self):
        terminal = self.terminal()
        terminal.retain_child_identity = True
        terminal._identity_lost = True
        terminal.child.returncode = None
        with unittest.mock.patch.object(harness.os, "close"), unittest.mock.patch.object(harness.os, "killpg") as kill:
            terminal.close()
            kill.assert_not_called()

    def test_drain_rejects_live_child(self):
        terminal = self.terminal()
        terminal.child.poll.return_value = None
        with unittest.mock.patch.object(harness.os, "close") as close:
            with self.assertRaisesRegex(RuntimeError, "before child exit"):
                terminal.drain_eof()
            close.assert_not_called()

    def test_drain_closes_parent_slave_before_eof(self):
        terminal = self.terminal()
        with unittest.mock.patch.object(harness.os, "close") as close, \
                unittest.mock.patch.object(harness.select, "select", return_value=([20], [], [])), \
                unittest.mock.patch.object(harness.os, "read", side_effect=OSError(errno.EIO, "closed PTY")):
            terminal.drain_eof()
            close.assert_called_once_with(21)
            self.assertTrue(terminal.eof and terminal._slave_closed)


class DriverCleanupTests(unittest.TestCase):
    def child(self, pid=321):
        child = unittest.mock.Mock(pid=pid, fixture_stdout_eof=True, fixture_stderr_eof=True)
        child.poll.return_value = 0
        return child

    def worker(self):
        worker = unittest.mock.Mock()
        worker.is_alive.return_value = True
        worker.join.side_effect = lambda **_kwargs: setattr(worker.is_alive, "return_value", False)
        return worker

    def run_driver(self, driver, *, children=(), failure=None, terminal=None, server=None, worker=None,
                   signal_number=None, recheck_error=None, kill_error=None, replace_root=False,
                   expected_interrupt=None, probe_error=None, root_error=None, alarm_error=None, fixture_capture=None):
        """Run the real entrypoint with no child, thread, socket, alarm, or real signal."""
        server = server if server is not None else unittest.mock.Mock(server_port=54321)
        worker = worker if worker is not None else self.worker()
        probe = unittest.mock.Mock()
        probe.connect_ex.return_value = errno.ECONNREFUSED
        probe.connect_ex.side_effect = probe_error
        callbacks = {}
        previous_handlers = {selected: object() for selected in (signal.SIGALRM, signal.SIGTERM, signal.SIGINT)}
        current_handlers = previous_handlers.copy()
        stdout, stderr = io.StringIO(), io.StringIO()
        with tempfile.TemporaryDirectory(prefix="driver-cleanup-control-") as directory, ExitStack() as stack:
            root = Path(directory) / "owned"
            root.mkdir()

            def register(selected_signal, callback):
                previous = current_handlers[selected_signal]
                current_handlers[selected_signal] = callback
                if callable(callback):
                    callbacks[selected_signal] = callback
                return previous

            remove = driver.shutil.rmtree

            def remove_root(path, *args, **kwargs):
                if Path(path) == root and root_error is not None:
                    raise root_error
                return remove(path, *args, **kwargs)

            def alarm(_kind, seconds):
                if seconds == 0 and alarm_error is not None:
                    raise alarm_error

            def locale(_requested, _env, owned):
                owned.extend(children)
                if replace_root:
                    root.rename(root.with_name("original"))
                    root.mkdir()
                if signal_number is not None:
                    callbacks[signal_number](signal_number, None)
                if terminal is None and fixture_capture is None:
                    raise failure if failure is not None else RuntimeError("synthetic execution failure")
                return "C.UTF-8", "synthetic"

            def install(_command, *, cwd, **_kwargs):
                for name in (".bashrc", ".bash_profile"):
                    path = cwd / name
                    data = path.read_bytes()
                    if b"# >>> openai image picker" not in data:
                        path.write_bytes(data + b"# >>> openai image picker\n")
                return SimpleNamespace(returncode=0, stdout=b"", stdout_eof=True, stderr_eof=True,
                                       stderr=b"Tab setup saved for future terminals. Existing custom bindings are preserved.\n")

            argv = [str(driver.__file__), str(Path(__file__).resolve()), str(Path(__file__).resolve())]
            if driver is startup:
                argv.append("login")
                stack.enter_context(unittest.mock.patch.object(startup, "SYSTEM_STARTUP_FILES", ()))
                stack.enter_context(unittest.mock.patch.object(startup, "recheck_system_sources",
                                                               return_value=([], True), side_effect=recheck_error))
            stack.enter_context(unittest.mock.patch.object(driver.sys, "argv", argv))
            stack.enter_context(unittest.mock.patch.object(driver.tempfile, "mkdtemp", return_value=str(root)))
            stack.enter_context(unittest.mock.patch.object(driver.time, "monotonic", return_value=1))
            stack.enter_context(unittest.mock.patch.object(driver.signal, "signal", side_effect=register))
            timer = stack.enter_context(unittest.mock.patch.object(driver.signal, "setitimer", side_effect=alarm))
            stack.enter_context(unittest.mock.patch.object(driver.shutil, "rmtree", side_effect=remove_root))
            kill = stack.enter_context(unittest.mock.patch.object(driver.os, "killpg", side_effect=kill_error))
            stack.enter_context(unittest.mock.patch.object(driver.picker, "fixture_utf8_locale", side_effect=locale))
            capture = stack.enter_context(unittest.mock.patch.object(driver.picker, "capture_fixture",
                                                                     side_effect=fixture_capture or install))
            launch = stack.enter_context(unittest.mock.patch.object(driver.picker, "Terminal", return_value=terminal))
            stack.enter_context(unittest.mock.patch.object(models.http.server, "HTTPServer", return_value=server))
            stack.enter_context(unittest.mock.patch.object(models.threading, "Thread", return_value=worker))
            sockets = stack.enter_context(unittest.mock.patch.object(models.socket, "socket"))
            sockets.return_value.__enter__.return_value = probe
            with redirect_stdout(stdout), redirect_stderr(stderr):
                if expected_interrupt is None:
                    status = driver.main()
                else:
                    with self.assertRaises(type(expected_interrupt)) as caught:
                        driver.main()
                    self.assertIs(caught.exception, expected_interrupt)
                    if isinstance(expected_interrupt, SystemExit):
                        self.assertEqual(caught.exception.code, expected_interrupt.code)
            retained = root.exists()
            if terminal is None:
                launch.assert_not_called()
                if fixture_capture is None:
                    capture.assert_not_called()
        self.assertEqual(current_handlers, previous_handlers)
        self.assertEqual(timer.call_args_list, [unittest.mock.call(signal.ITIMER_REAL, 115 if driver is startup else 25),
                                               unittest.mock.call(signal.ITIMER_REAL, 0)])
        if expected_interrupt is not None:
            self.assertEqual(stdout.getvalue(), "")
            self.assertEqual(stderr.getvalue(), "")
            return SimpleNamespace(retained=retained, server=server, worker=worker,
                                   probe=probe, sockets=sockets, kill=kill)
        lines = stdout.getvalue().splitlines()
        self.assertEqual(status, 1)
        self.assertEqual(len(lines), 1)
        self.assertTrue(lines[0].startswith("RECEIPT: "))
        encoded = lines[0][len("RECEIPT: "):]
        self.assertLessEqual(len(encoded.encode()), 8 * startup.LIMIT if driver is startup else 65536)
        report = json.loads(encoded)
        self.assertEqual(report["exit_code"], 1)
        self.assertTrue(report["failures"])
        self.assertEqual(stderr.getvalue(), "FAIL: " + report["case"] + "\n")
        with self.assertRaises(RuntimeError):
            pilot.require_driver_pass(stdout.getvalue().encode(), stderr.getvalue().encode(), status,
                                      {report["case"]}, report["case"])
        return SimpleNamespace(report=report, retained=retained, server=server, worker=worker,
                               probe=probe, sockets=sockets, kill=kill)

    def test_execution_failures_keep_receipt_and_owned_cleanup(self):
        for driver in (startup, models):
            for error in (OSError("synthetic failure"), RuntimeError("x" * 30000)):
                with self.subTest(driver=driver.__name__, error=type(error).__name__):
                    child = self.child()
                    got = self.run_driver(driver, children=[child], failure=error)
                    child.wait.assert_called_once_with(timeout=2)
                    self.assertTrue(got.report["cleanup_complete"])
                    self.assertFalse(got.retained)
                    self.assertEqual(got.report["failures"][0], str(error)[:2000])

    def test_signal_callbacks_keep_timeout_failure_status(self):
        for driver in (startup, models):
            for selected_signal in (signal.SIGINT, signal.SIGTERM, signal.SIGALRM):
                with self.subTest(driver=driver.__name__, signal=selected_signal):
                    child = self.child()
                    got = self.run_driver(driver, children=[child], signal_number=selected_signal)
                    self.assertIn("fixture interrupted", got.report["failures"][0])
                    child.wait.assert_called_once_with(timeout=2)
                    self.assertTrue(got.report["cleanup_complete"])

    def test_process_statuses_distinguish_control_exceptions_and_real_signals(self):
        program = r"""
import importlib.util, json, os, signal, sys
from pathlib import Path
from unittest.mock import Mock, patch
from contextlib import ExitStack
script, mode, directory = sys.argv[1:]
root = Path(directory) / "owned"
root.mkdir()
spec = importlib.util.spec_from_file_location("driver", script)
driver = importlib.util.module_from_spec(spec)
spec.loader.exec_module(driver)
child = Mock(pid=321, fixture_stdout_eof=True, fixture_stderr_eof=True)
child.poll.return_value = 0
server = Mock(server_port=54321)
worker = Mock()
worker.is_alive.return_value = True
worker.join.side_effect = lambda **_: setattr(worker.is_alive, "return_value", False)
probe = Mock()
probe.connect_ex.return_value = 111
selected = (signal.SIGALRM, signal.SIGTERM, signal.SIGINT)
previous = {sig: signal.getsignal(sig) for sig in selected}

def interrupt(_requested, _env, owned):
    owned.append(child)
    if mode == "KeyboardInterrupt":
        raise KeyboardInterrupt("synthetic control")
    if mode == "SystemExit":
        raise SystemExit(7)
    os.kill(os.getpid(), getattr(signal, mode))
    raise AssertionError("signal handler returned")

with ExitStack() as stack:
    stack.enter_context(patch.object(driver.tempfile, "mkdtemp", return_value=str(root)))
    stack.enter_context(patch.object(driver.signal, "setitimer"))
    stack.enter_context(patch.object(driver.picker, "fixture_utf8_locale", side_effect=interrupt))
    argv = [script, script, script]
    if "shell-startup" in script:
        argv.append("login")
        stack.enter_context(patch.object(driver, "SYSTEM_STARTUP_FILES", ()))
        stack.enter_context(patch.object(driver, "recheck_system_sources", return_value=([], True)))
    else:
        probe.connect_ex.return_value = driver.errno.ECONNREFUSED
        stack.enter_context(patch.object(driver.http.server, "HTTPServer", return_value=server))
        stack.enter_context(patch.object(driver.threading, "Thread", return_value=worker))
        sockets = stack.enter_context(patch.object(driver.socket, "socket"))
        sockets.return_value.__enter__.return_value = probe
    stack.enter_context(patch.object(driver.sys, "argv", argv))
    try:
        status = driver.main()
    finally:
        Path(directory, "cleanup.json").write_text(json.dumps({
            "root_removed": not root.exists(), "child_waits": child.wait.call_count,
            "server_closed": server.server_close.call_count, "worker_joined": worker.join.call_count,
            "listener_probes": probe.connect_ex.call_count,
            "signals_restored": all(signal.getsignal(sig) == previous[sig] for sig in selected)}))
raise SystemExit(status)
"""
        for driver in (startup, models):
            for mode in ("KeyboardInterrupt", "SystemExit", "SIGINT", "SIGTERM", "SIGALRM"):
                with self.subTest(driver=driver.__name__, mode=mode), tempfile.TemporaryDirectory() as directory:
                    got = subprocess.run([sys.executable, "-I", "-B", "-c", program, str(driver.__file__), mode, directory],
                                         stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5, check=False)
                    cleanup = json.loads(Path(directory, "cleanup.json").read_text())
                    self.assertTrue(cleanup["root_removed"])
                    self.assertTrue(cleanup["signals_restored"])
                    self.assertEqual(cleanup["child_waits"], 1)
                    for field in ("server_closed", "worker_joined", "listener_probes"):
                        self.assertEqual(cleanup[field], 1 if driver is models else 0)
                    self.assertNotIn(b"PASS:", got.stdout + got.stderr)
                    if mode == "KeyboardInterrupt":
                        self.assertIn(got.returncode, (-signal.SIGINT, 128 + signal.SIGINT))
                        self.assertEqual(got.stdout, b"")
                        self.assertIn(b"KeyboardInterrupt: synthetic control", got.stderr)
                    elif mode == "SystemExit":
                        self.assertEqual((got.returncode, got.stdout, got.stderr), (7, b"", b""))
                    else:
                        self.assertEqual(got.returncode, 1)
                        lines = got.stdout.decode().splitlines()
                        self.assertEqual(len(lines), 1)
                        self.assertTrue(lines[0].startswith("RECEIPT: "))
                        report = json.loads(lines[0][len("RECEIPT: "):])
                        self.assertEqual(report["exit_code"], 1)
                        self.assertTrue(report["cleanup_complete"])
                        self.assertIn("fixture interrupted", report["failures"][0])
                        self.assertEqual(got.stderr, ("FAIL: " + report["case"] + "\n").encode())

    def test_terminal_failure_does_not_skip_owned_children(self):
        for error in (OSError("terminal close"),):
            with self.subTest(error=type(error).__name__):
                terminal = unittest.mock.Mock(raw=bytearray(), events=[], child=SimpleNamespace(returncode=None))
                terminal.wait.side_effect = OSError("synthetic prompt failure")
                terminal.close.side_effect = error
                child = self.child()
                got = self.run_driver(startup, children=[child], terminal=terminal)
                self.assertEqual(terminal.close.call_count, 2)
                child.wait.assert_called_once_with(timeout=2)
                self.assertTrue(got.retained)
                self.assertFalse(got.report["cleanup_complete"])
                self.assertIn("terminal cleanup: " + str(error), got.report["failures"])

    def test_child_failure_does_not_skip_later_owned_children(self):
        for driver in (startup, models):
            for error in (OSError("child wait"), None):
                with self.subTest(driver=driver.__name__, error=type(error).__name__):
                    first, second = self.child(), self.child(654)
                    first.wait.side_effect = error
                    first.fixture_stderr_eof = error is not None
                    second.poll.return_value = None
                    got = self.run_driver(driver, children=[first, second])
                    first.wait.assert_called_once_with(timeout=2)
                    second.wait.assert_called_once_with(timeout=2)
                    got.kill.assert_called_once_with(second.pid, signal.SIGKILL)
                    self.assertTrue(got.retained)
                    self.assertFalse(got.report["cleanup_complete"])

    def test_disappeared_owned_group_still_requires_join_and_eof(self):
        for driver in (startup, models):
            for eof in (True, False):
                with self.subTest(driver=driver.__name__, eof=eof):
                    child = self.child()
                    child.poll.return_value = None
                    child.fixture_stderr_eof = eof
                    got = self.run_driver(driver, children=[child], kill_error=ProcessLookupError())
                    got.kill.assert_called_once_with(child.pid, signal.SIGKILL)
                    child.wait.assert_called_once_with(timeout=2)
                    self.assertEqual(got.report["cleanup_complete"], eof)
                    self.assertEqual(got.retained, not eof)

    def test_fixture_failure_does_not_skip_later_cleanup(self):
        for stage in ("shutdown", "server_close"):
            for error in (OSError("fixture failure"),):
                with self.subTest(stage=stage, error=type(error).__name__):
                    server, worker = unittest.mock.Mock(server_port=54321), self.worker()
                    getattr(server, stage).side_effect = error
                    order = unittest.mock.Mock()
                    order.attach_mock(server.shutdown, "shutdown")
                    order.attach_mock(server.server_close, "close")
                    order.attach_mock(worker.join, "join")
                    got = self.run_driver(models, server=server, worker=worker)
                    self.assertEqual(order.mock_calls, [unittest.mock.call.shutdown(), unittest.mock.call.close(),
                                                        unittest.mock.call.join(timeout=2)])
                    self.assertTrue(got.report["fixture_thread_joined"])
                    got.probe.settimeout.assert_called_once_with(1)
                    self.assertTrue(got.retained)
                    self.assertFalse(got.report["cleanup_complete"])

    def test_all_fixture_cleanup_failures_retain_their_diagnostics(self):
        server, worker = unittest.mock.Mock(server_port=54321), self.worker()
        server.shutdown.side_effect = OSError("shutdown failed")
        server.server_close.side_effect = OSError("close failed")
        worker.join.side_effect = OSError("join failed")
        got = self.run_driver(models, server=server, worker=worker)
        self.assertIn("fixture shutdown: shutdown failed", got.report["failures"])
        self.assertIn("fixture close: close failed", got.report["failures"])
        self.assertIn("fixture join: join failed", got.report["failures"])
        worker.join.assert_called_once_with(timeout=2)
        got.sockets.assert_not_called()
        self.assertFalse(got.report["fixture_thread_joined"])
        self.assertTrue(got.retained)

    def test_live_fixture_worker_prevents_probe_and_root_removal(self):
        worker = self.worker()
        worker.join.side_effect = None
        got = self.run_driver(models, worker=worker)
        worker.join.assert_called_once_with(timeout=2)
        got.sockets.assert_not_called()
        self.assertFalse(got.report["fixture_thread_joined"])
        self.assertFalse(got.report["cleanup_complete"])
        self.assertTrue(got.retained)

    def test_source_recheck_failure_still_cleans_owned_children(self):
        for error in (OSError("recheck failed"),):
            with self.subTest(error=type(error).__name__):
                child = self.child()
                got = self.run_driver(startup, children=[child], recheck_error=error)
                child.wait.assert_called_once_with(timeout=2)
                self.assertIn("system source recheck: " + str(error), got.report["failures"])
                self.assertTrue(got.report["cleanup_complete"])
                self.assertFalse(got.retained)

    def test_execution_interrupts_propagate_after_owned_cleanup(self):
        for driver in (startup, models):
            for error in (KeyboardInterrupt("execution interrupted"), SystemExit(7)):
                with self.subTest(driver=driver.__name__, error=type(error).__name__):
                    child = self.child()
                    got = self.run_driver(driver, children=[child], failure=error, expected_interrupt=error)
                    child.wait.assert_called_once_with(timeout=2)
                    self.assertFalse(got.retained)
                    if driver is models:
                        got.server.shutdown.assert_called_once_with()
                        got.server.server_close.assert_called_once_with()
                        got.worker.join.assert_called_once_with(timeout=2)
                        got.probe.connect_ex.assert_called_once_with(("127.0.0.1", 54321))

    def test_capture_interrupt_survives_cleanup_failure_through_driver_entrypoint(self):
        for driver in (startup, models):
            for error in (KeyboardInterrupt("capture interrupted"), SystemExit(7), GeneratorExit("capture closed")):
                with self.subTest(driver=driver.__name__, error=type(error).__name__):
                    fake = Capture([b"x"], [], code=None, read_error=error)
                    failure = OSError("stdout close failed")
                    fake.process.stdout.close = unittest.mock.Mock(side_effect=failure)

                    def capture(_argv, *, owned_processes, **_kwargs):
                        return fake.run(owned_processes=owned_processes)

                    got = self.run_driver(driver, expected_interrupt=error, fixture_capture=capture)
                    self.assertIs(error.__cause__, failure)
                    self.assertEqual(error.fixture_cleanup_errors, (failure,))
                    self.assertEqual(fake.waits, [2, 2])
                    self.assertTrue(fake.process.stderr.closed)
                    self.assertTrue(got.retained)

    def test_fresh_shell_interrupt_survives_ordinary_terminal_cleanup_failure(self):
        for error in (KeyboardInterrupt("prompt interrupted"), SystemExit(7)):
            with self.subTest(error=type(error).__name__):
                terminal = unittest.mock.Mock(raw=bytearray(), events=[], child=SimpleNamespace(returncode=None))
                terminal.wait.side_effect = error
                failure = OSError("terminal close failed")
                terminal.close.side_effect = failure
                child = self.child()
                got = self.run_driver(startup, children=[child], terminal=terminal, expected_interrupt=error)
                self.assertIs(error.__cause__, failure)
                self.assertEqual(error.fixture_cleanup_errors, (failure,))
                self.assertEqual(terminal.close.call_count, 2)
                child.wait.assert_called_once_with(timeout=2)
                self.assertTrue(got.retained)

    def test_incomplete_capture_cleanup_retains_private_root(self):
        for driver in (startup, models):
            with self.subTest(driver=driver.__name__):
                child = self.child()
                child.fixture_cleanup_complete = False
                got = self.run_driver(driver, children=[child])
                self.assertFalse(got.report["cleanup_complete"])
                self.assertTrue(got.retained)
                self.assertIn("child pipes: fixture pipe cleanup was incomplete", got.report["failures"])

    def test_terminal_interrupts_propagate_after_owned_child_cleanup(self):
        for error in (KeyboardInterrupt("terminal close"), SystemExit(7)):
            with self.subTest(error=type(error).__name__):
                terminal = unittest.mock.Mock(raw=bytearray(), events=[], child=SimpleNamespace(returncode=None))
                terminal.wait.side_effect = OSError("synthetic prompt failure")
                terminal.close.side_effect = error
                child = self.child()
                got = self.run_driver(startup, children=[child], terminal=terminal, expected_interrupt=error)
                self.assertEqual(terminal.close.call_count, 2)
                child.wait.assert_called_once_with(timeout=2)
                self.assertTrue(got.retained)

    def test_child_interrupts_propagate_after_later_cleanup(self):
        for driver in (startup, models):
            for stage in ("kill", "wait"):
                for error in (KeyboardInterrupt("child interrupted"), SystemExit(7)):
                    with self.subTest(driver=driver.__name__, stage=stage, error=type(error).__name__):
                        first, second = self.child(), self.child(654)
                        if stage == "wait":
                            first.wait.side_effect = error
                        else:
                            first.poll.return_value = None
                        got = self.run_driver(driver, children=[first, second], expected_interrupt=error,
                                              kill_error=error if stage == "kill" else None)
                        first.wait.assert_called_once_with(timeout=2)
                        second.wait.assert_called_once_with(timeout=2)
                        self.assertTrue(got.retained)
                        if driver is models:
                            got.worker.join.assert_called_once_with(timeout=2)
                            got.probe.connect_ex.assert_called_once()

    def test_fixture_interrupts_propagate_after_later_cleanup(self):
        for stage in ("shutdown", "server_close", "join", "listener"):
            for error in (KeyboardInterrupt("fixture interrupted"), SystemExit(7)):
                with self.subTest(stage=stage, error=type(error).__name__):
                    server, worker = unittest.mock.Mock(server_port=54321), self.worker()
                    if stage in ("shutdown", "server_close"):
                        getattr(server, stage).side_effect = error
                    elif stage == "join":
                        worker.join.side_effect = error
                    got = self.run_driver(models, server=server, worker=worker, expected_interrupt=error,
                                          probe_error=error if stage == "listener" else None)
                    server.shutdown.assert_called_once_with()
                    server.server_close.assert_called_once_with()
                    worker.join.assert_called_once_with(timeout=2)
                    if stage == "join":
                        got.sockets.assert_not_called()
                    else:
                        got.probe.connect_ex.assert_called_once_with(("127.0.0.1", 54321))
                    self.assertTrue(got.retained)

    def test_recheck_interrupts_propagate_after_resource_cleanup(self):
        for error in (KeyboardInterrupt("recheck interrupted"), SystemExit(7)):
            with self.subTest(error=type(error).__name__):
                child = self.child()
                got = self.run_driver(startup, children=[child], recheck_error=error, expected_interrupt=error)
                child.wait.assert_called_once_with(timeout=2)
                self.assertFalse(got.retained)

    def test_alarm_and_root_interrupts_propagate_with_signal_restoration(self):
        for driver in (startup, models):
            for stage in ("alarm", "root"):
                for error in (KeyboardInterrupt("cleanup interrupted"), SystemExit(7)):
                    with self.subTest(driver=driver.__name__, stage=stage, error=type(error).__name__):
                        child = self.child()
                        got = self.run_driver(driver, children=[child], expected_interrupt=error,
                                              alarm_error=error if stage == "alarm" else None,
                                              root_error=error if stage == "root" else None)
                        child.wait.assert_called_once_with(timeout=2)
                        self.assertTrue(got.retained)

    def test_ordinary_cleanup_failure_does_not_replace_execution_interrupt(self):
        for driver in (startup, models):
            for error in (KeyboardInterrupt("execution interrupted"), SystemExit(7)):
                with self.subTest(driver=driver.__name__, error=type(error).__name__):
                    first, second = self.child(), self.child(654)
                    first.wait.side_effect = OSError("wait failed")
                    got = self.run_driver(driver, children=[first, second], failure=error, expected_interrupt=error)
                    second.wait.assert_called_once_with(timeout=2)
                    self.assertTrue(got.retained)

    def test_replaced_private_root_is_retained(self):
        for driver in (startup, models):
            with self.subTest(driver=driver.__name__):
                got = self.run_driver(driver, replace_root=True)
                self.assertFalse(got.report["cleanup_complete"])
                self.assertTrue(got.retained)
                self.assertIn("root cleanup: ", got.report["failures"])


class RefusedStartupContractTests(unittest.TestCase):
    def result(self, code=0, stdout=b"", stderr=b""):
        return SimpleNamespace(returncode=code, stdout=stdout, stderr=stderr, stdout_eof=True, stderr_eof=True)

    def export_result(self, directory, profiles):
        fixtures = []
        for profile in profiles:
            script = "# Synthetic candidate fixture for " + profile.name + "\n"
            profile_hash = startup.hashlib.sha256(str(profile).encode()).hexdigest()[:16]
            script_hash = startup.hashlib.sha256(script.encode()).hexdigest()
            name = f"picker-bash-{profile_hash}-{script_hash}.bash"
            fixtures.append({"profile": str(profile), "script_name": name, "script": script,
                             "block": "\n# >>> openai image picker\n# " + name + "\n# <<< openai image picker\n"})
        value = {"contract": "seeded-existing-state", "shell": "bash", "directory": str(directory), "fixtures": fixtures}
        return value, self.encoded_export(value)

    def encoded_export(self, value):
        output = ("=== RUN   TestPickerStartupFixtureExport\nPICKER_STARTUP_FIXTURE " + json.dumps(value) +
                  "\n--- PASS: TestPickerStartupFixtureExport (0.00s)\nPASS\n")
        return self.result(stdout=output.encode())

    def test_refusal_requires_exact_status_message_and_pipe_eof(self):
        for action in ("--install-picker", "--uninstall-picker"):
            for error_format in ("text", "json"):
                with self.subTest(action=action, format=error_format):
                    message = startup.refusal_message(action)
                    stderr = ((message + "\n") if error_format == "text" else json.dumps({"message": message})).encode()
                    good = self.result(1, stderr=stderr)
                    startup.require_setup_result(good, action, True, error_format)
                    for field, value in (("returncode", 0), ("returncode", 7), ("stdout", b"unexpected"),
                                         ("stderr", b"generic failure\n"), ("stdout_eof", False), ("stderr_eof", False)):
                        bad = deepcopy(good)
                        setattr(bad, field, value)
                        with self.subTest(field=field), self.assertRaises((AssertionError, ValueError)):
                            startup.require_setup_result(bad, action, True, error_format)

    def test_structured_refusal_rejects_extra_keys_duplicates_and_trailing_prose(self):
        message = json.dumps(startup.refusal_message("--install-picker"))
        for stderr in ('{"message":' + message + ',"extra":true}',
                       '{"message":' + message + ',"message":' + message + '}',
                       '{"message":' + message + '}\nadditional diagnostic'):
            with self.subTest(stderr=stderr), self.assertRaises((AssertionError, ValueError)):
                startup.require_setup_result(self.result(1, stderr=stderr.encode()), "--install-picker", True, "json")

    def test_exporter_binds_owned_paths_content_and_test_completion(self):
        directory = Path("/synthetic/config/openai/shell")
        profiles = [Path("/synthetic/.bashrc"), Path("/synthetic/.bash_profile")]
        value, good = self.export_result(directory, profiles)
        self.assertEqual(startup.exported_fixtures(good, directory, profiles), value["fixtures"])
        mutations = [
            lambda data: data.update(contract="installed"),
            lambda data: data.update(directory="/outside/shell"),
            lambda data: data.update(shell="zsh"),
            lambda data: data["fixtures"].pop(),
            lambda data: data["fixtures"][0].update(profile="/outside/.bashrc"),
            lambda data: data["fixtures"][0].update(script_name="../escape.bash"),
            lambda data: data["fixtures"][0].update(script="changed bytes"),
            lambda data: data["fixtures"][0].update(block="missing marker"),
            lambda data: data["fixtures"][0].update(block="x" * (startup.SMALL + 1)),
        ]
        for index, mutate in enumerate(mutations):
            bad = deepcopy(value)
            mutate(bad)
            with self.subTest(mutation=index), self.assertRaises(AssertionError):
                startup.exported_fixtures(self.encoded_export(bad), directory, profiles)
        for field, value in (("returncode", 1), ("stderr", b"failure"), ("stdout_eof", False),
                             ("stderr_eof", False), ("stdout", good.stdout.replace(b"--- PASS:", b"--- SKIP:")),
                             ("stdout", good.stdout + b"unexpected\n"), ("stdout", b"x" * (startup.LIMIT + 1))):
            bad = deepcopy(good)
            setattr(bad, field, value)
            with self.subTest(field=field), self.assertRaises(AssertionError):
                startup.exported_fixtures(bad, directory, profiles)

    def test_refused_contract_requires_exporter_before_resource_creation(self):
        with unittest.mock.patch.object(startup.sys, "argv", ["fixture", "cli", "bash", "login", "--setup-contract", "refused"]), \
                unittest.mock.patch.object(startup.tempfile, "mkdtemp") as create, redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit) as raised:
                startup.main()
            self.assertEqual(raised.exception.code, 2)
            create.assert_not_called()

    def run_lifecycle(self, mode, fault=None):
        """Exercise lifecycle decisions with synthetic captures and terminal observations."""
        calls = []
        counters = {"install": 0, "remove": 0}
        stdout, stderr = io.StringIO(), io.StringIO()
        binary, bash, exporter = str(Path(startup.__file__).resolve()), str(Path(__file__).resolve()), str(Path(models.__file__).resolve())

        class Terminal:
            def __init__(self, _bash, args, env, **_kwargs):
                self.raw, self.events = bytearray(startup.PROMPT.encode()), []
                self.child, self.eof = SimpleNamespace(returncode=None), False
                name = "bash_profile" if "--login" in args else "bashrc"
                profile = Path(env["HOME"]) / ("." + name)
                active = b"# >>> openai image picker" in profile.read_bytes()
                output = ("\nSENTINEL:" + name + "\nEXECUTABLE:" + env["OPENAI_PROFILE_BIN"] + "/openai" +
                          "\nVERSION:5.2.15\nINTEGRATION:" + ("bash" if active else "") + "\n")
                if active:
                    output += 'complete -o filenames -F __openai_picker_complete openai\n"\\C-i": "\\e[99;1\\C-i\\e[99;2~"\n'
                self.probe = (output + startup.DONE + "\n").encode()

            def wait(self, marker, **_kwargs):
                if marker == startup.DONE:
                    self.raw.extend(self.probe)

            def send(self, data):
                if data == b"finish\r":
                    self.child.returncode = 0

            def running(self):
                return self.child.returncode is None

            def text(self):
                return self.raw.decode()

            def drain_eof(self):
                self.eof = True

            def close(self):
                pass

        def capture(command, *, env, cwd, **_kwargs):
            calls.append(command)
            if command[0] == exporter:
                request = json.loads(env["OPENAI_CLI_PICKER_FIXTURE_REQUEST"])
                return self.export_result(Path(request["directory"]), [Path(path) for path in request["profiles"]])[1]
            action = command[-1]
            if action == "--install-picker":
                counters["install"] += 1
                refused = counters["install"] <= 3
                if fault == "accept-install" and counters["install"] == 1:
                    refused = False
                if fault == "rewrite-refusal" and counters["install"] == 1:
                    profile = cwd / ".bashrc"
                    profile.write_bytes(profile.read_bytes() + b"# unexpected rewrite\n")
                if fault == "rewrite-noop" and counters["install"] == 4:
                    script = next((Path(env["XDG_CONFIG_HOME"]) / "openai/shell").glob("picker-bash-*"))
                    script.write_bytes(script.read_bytes() + b"# unexpected rewrite\n")
            else:
                counters["remove"] += 1
                refused = counters["remove"] <= 2
                marker = cwd / ".openai/shell/image-picker.tab-off-bash"
                marker.parent.mkdir(parents=True, exist_ok=True)
                marker.touch(mode=0o600)
                if fault == "erase-recovery" and counters["remove"] == 1:
                    next(cwd.glob(".openai-picker-recovery-*/original")).unlink()
                if not refused and fault != "retain-script":
                    for script in (Path(env["XDG_CONFIG_HOME"]) / "openai/shell").glob("picker-bash-*"):
                        script.unlink()
            if refused:
                message = startup.refusal_message(action)
                if "--format-error" in command:
                    return self.result(1, stderr=(json.dumps({"message": message}) + "\n").encode())
                return self.result(1, stderr=(message + "\n").encode())
            message = ("Tab setup saved for future terminals. Existing custom bindings are preserved.\n"
                       if action == "--install-picker" else "Tab shortcut setup removed. Open a new terminal to finish.\n")
            return self.result(stderr=message.encode())

        with tempfile.TemporaryDirectory() as directory, ExitStack() as stack:
            root = Path(directory) / "owned"
            root.mkdir()
            argv = ["fixture", binary, bash, mode, "--setup-contract", "refused", "--autocomplete-test-binary", exporter]
            if fault == "first-use-cleanup":
                argv.append("--first-use-admission")

                def failed_admission(_binary, _bash, _env, _deadline, report):
                    report["first_use_admission"] = {"cleanup_complete": False}
                    close = unittest.mock.Mock(side_effect=OSError("first-use terminal cleanup failed"))
                    try:
                        close()
                    finally:
                        try:
                            startup.picker.cleanup_fixture_resources(close)
                        finally:
                            self.assertEqual(close.call_count, 2)

                stack.enter_context(unittest.mock.patch.object(startup, "first_use_admission", side_effect=failed_admission))
            stack.enter_context(unittest.mock.patch.object(startup.sys, "argv", argv))
            stack.enter_context(unittest.mock.patch.object(startup.tempfile, "mkdtemp", return_value=str(root)))
            stack.enter_context(unittest.mock.patch.object(startup, "SYSTEM_STARTUP_FILES", ()))
            stack.enter_context(unittest.mock.patch.object(startup, "recheck_system_sources", return_value=([], True)))
            stack.enter_context(unittest.mock.patch.object(startup.signal, "signal"))
            timer = stack.enter_context(unittest.mock.patch.object(startup.signal, "setitimer"))
            stack.enter_context(unittest.mock.patch.object(startup.time, "monotonic", return_value=1))
            stack.enter_context(unittest.mock.patch.object(startup.picker, "fixture_utf8_locale", return_value=("C.UTF-8", "synthetic")))
            stack.enter_context(unittest.mock.patch.object(startup.picker, "capture_fixture", side_effect=capture))
            stack.enter_context(unittest.mock.patch.object(startup.picker, "Terminal", Terminal))
            with redirect_stdout(stdout), redirect_stderr(stderr):
                status = startup.main()
            self.assertEqual(root.exists(), fault == "first-use-cleanup")
        timer.assert_has_calls([unittest.mock.call(signal.ITIMER_REAL, 115), unittest.mock.call(signal.ITIMER_REAL, 0)])
        lines = stdout.getvalue().splitlines()
        report = json.loads(lines[0][len("RECEIPT: "):])
        self.assertLessEqual(len(lines[0].encode()), 8 * startup.LIMIT + len("RECEIPT: "))
        return status, report, lines[1:], stderr.getvalue(), calls

    def test_refused_lifecycle_has_separate_cases_and_exact_checkpoints(self):
        for mode in ("login", "nonlogin"):
            with self.subTest(mode=mode):
                status, report, markers, stderr, calls = self.run_lifecycle(mode)
                self.assertEqual((status, markers, stderr), (0, ["PASS: bash-" + mode + "-refused"], ""))
                self.assertEqual(report["setup_contract"], "refused")
                self.assertEqual([item["action"] for item in report["setup_calls"]],
                                 ["install-refused-text", "install-refused-json", "install-refused-again", "seeded-install-noop",
                                  "seeded-remove-refused-text", "seeded-remove-refused-json", "absent-profile-remove"])
                self.assertEqual([item["exit_code"] for item in report["setup_calls"]], [1, 1, 1, 0, 1, 1, 0])
                self.assertEqual([(item["checkpoint"], item["expected_active"]) for item in report["fresh_shells"]],
                                 [("fresh-original", False), ("fresh-seeded-existing", True), ("fresh-after-refused-remove", True)])
                for field in ("original_profiles_unchanged", "seeded_profiles_unchanged", "recovery_evidence_unchanged",
                              "absent_profiles_preserved", "opt_out_created", "generated_scripts_removed", "unselected_profile_unchanged",
                              "cleanup_complete", "setup_stdout_eof", "setup_stderr_eof", "pty_eof"):
                    self.assertIs(report[field], True)
                self.assertEqual(report["setup_call_count"], 7)
                self.assertEqual(report["fresh_shell_count"], 3)
                self.assertEqual(len(calls), 8)
                self.assertEqual(report["seeded_existing_state"]["contract"], "seeded-existing-state")
                self.assertEqual(report["installation_scope"], "Seeded existing state tests activation and cleanup, not installation.")
                self.assertEqual(report["automatic_setup_scope"], "Not exercised: private HOME differs from the account home.")

    def test_refused_lifecycle_rejects_success_and_state_loss(self):
        for fault in ("accept-install", "rewrite-refusal", "rewrite-noop", "erase-recovery", "retain-script"):
            with self.subTest(fault=fault):
                status, report, markers, stderr, _calls = self.run_lifecycle("login", fault)
                self.assertEqual(status, 1)
                self.assertEqual(markers, [])
                self.assertEqual(stderr, "FAIL: bash-login-refused\n")
                self.assertTrue(report["failures"])

    def test_failed_first_use_cleanup_retains_shared_root_and_never_passes(self):
        status, report, markers, stderr, calls = self.run_lifecycle("nonlogin", "first-use-cleanup")
        self.assertEqual((status, markers, stderr, calls), (1, [], "FAIL: bash-nonlogin-refused\n", []))
        self.assertFalse(report["first_use_admission"]["cleanup_complete"])
        self.assertFalse(report["cleanup_complete"])
        self.assertIn("first-use terminal cleanup failed", report["failures"])


class FirstUseAdmissionTests(unittest.TestCase):
    @staticmethod
    def event(mask, name=None, watch=7):
        name = (name or ".openai-picker-" + "A" * 26).encode() + b"\0"
        name += b"\0" * (-len(name) % 4)
        return struct.pack("iIII", watch, mask, 0, len(name)) + name

    def test_admission_requires_observed_staging_and_complete_cleanup(self):
        events = startup.profile_events(self.event(0x100) + self.event(0x200), 7)
        self.assertEqual(startup.staging_events(events, True),
                         {"created": [".openai-picker-" + "A" * 26], "deleted": [".openai-picker-" + "A" * 26],
                          "sequence": events})
        self.assertEqual(startup.staging_events([], False), {"created": [], "deleted": [], "sequence": []})
        for values, expected in (([], True), (events, False), (events[:1], True), (events[1:], False)):
            with self.subTest(values=values, expected=expected), self.assertRaises(AssertionError):
                startup.staging_events(values, expected)

    def test_staging_rejects_delete_before_create_and_duplicate_pairs(self):
        create, delete = self.event(0x100), self.event(0x200)
        for data in (delete + create, create + delete + create + delete,
                     create + create + delete + delete, create + delete + delete):
            with self.subTest(data=data), self.assertRaises(AssertionError):
                startup.staging_events(startup.profile_events(data, 7), True)

    def test_unrelated_account_events_cannot_prove_admission(self):
        for name in (".bash_history", ".openai", ".openai-picker-recovery-example", ".openai-picker-short"):
            events = startup.profile_events(self.event(0x100, name) + self.event(0x200, name), 7)
            with self.subTest(name=name), self.assertRaises(AssertionError):
                startup.staging_events(events, True)

    def test_event_loss_truncation_wrong_watch_and_overflow_fail(self):
        valid = self.event(0x100)
        for data in (valid[:-1], valid[:15], self.event(0x4000, watch=-1), self.event(0x8000),
                     self.event(0x100, watch=8), valid * 257, b"x" * (startup.SMALL + 1)):
            with self.subTest(size=len(data)), self.assertRaises(RuntimeError):
                startup.profile_events(data, 7)
        for mask in (0x40, 0x80, 0x40000100):
            with self.subTest(mask=mask), self.assertRaises(AssertionError):
                startup.staging_events(startup.profile_events(self.event(mask), 7), True)

    def test_observer_drains_events_without_timing_polling_and_closes_once(self):
        observer = startup.ProfileObserver.__new__(startup.ProfileObserver)
        observer.fd, observer.watch = 99, 7
        with unittest.mock.patch.object(startup.os, "read", side_effect=[self.event(0x100), self.event(0x200), BlockingIOError]), \
                unittest.mock.patch.object(startup.os, "close") as close:
            startup.staging_events(observer.read(), True)
            observer.close()
            observer.close()
        close.assert_called_once_with(99)

    def test_first_use_rejects_host_execution_before_observer_or_child_creation(self):
        with unittest.mock.patch.object(startup.sys, "platform", "darwin"), \
                unittest.mock.patch.object(startup, "ProfileObserver") as observer, \
                unittest.mock.patch.object(startup.picker, "Terminal") as terminal:
            with self.assertRaisesRegex(RuntimeError, "disposable WSL account"):
                startup.first_use_admission("unused", "unused", {}, 10, {})
        observer.assert_not_called()
        terminal.assert_not_called()

    def test_first_use_flag_requires_refused_nonlogin_before_resource_creation(self):
        for mode, contract in (("login", "refused"), ("nonlogin", "supported")):
            argv = ["fixture", "cli", "bash", mode, "--setup-contract", contract,
                    "--autocomplete-test-binary", "exporter", "--first-use-admission"]
            with self.subTest(mode=mode, contract=contract), \
                    unittest.mock.patch.object(startup.sys, "argv", argv), \
                    unittest.mock.patch.object(startup.tempfile, "mkdtemp") as create, redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as raised:
                    startup.main()
                self.assertEqual(raised.exception.code, 2)
                create.assert_not_called()


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
            with unittest.mock.patch.object(startup.os, "open") as opened:
                with self.assertRaisesRegex(RuntimeError, "exceeds64KiB"):
                    startup.system_source(path)
                opened.assert_not_called()


if __name__ == "__main__":
    unittest.main()
