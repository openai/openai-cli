from contextlib import ExitStack
import json
import os
from pathlib import Path
import signal
import sys
import tempfile
import time
import unittest
from unittest.mock import ANY, Mock, patch

if os.name == "posix":
    import browser_guided_capture as capture


@unittest.skipUnless(os.name == "posix", "guided capture requires a POSIX PTY")
class GuidedCaptureCleanupTest(unittest.TestCase):
    def test_expired_deadline_still_allows_launcher_cleanup(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "capture"
            launcher = Path(directory) / "launcher.py"
            launcher.write_text(
                "import signal, sys\n"
                "def stop(*_):\n"
                "    sys.stdout.write('x' * 131072)\n"
                "    sys.stdout.flush()\n"
                "    sys.exit(0)\n"
                "signal.signal(signal.SIGTERM, stop)\n"
                "signal.alarm(15)\n"
                "print('launcher ready', flush=True)\n"
                "signal.pause()\n")
            original_terminal = capture.harness.Terminal
            terminal = None

            def expire_after_launch(*args, **kwargs):
                nonlocal terminal
                terminal = original_terminal(*args, **kwargs)
                self.addCleanup(terminal.close)
                terminal.wait("launcher ready")
                terminal.deadline = time.monotonic()
                return terminal

            argv = ["capture", sys.executable, str(output), "--launcher", str(launcher)]
            with patch.object(sys, "argv", argv), \
                 patch.object(capture.harness, "Terminal", side_effect=expire_after_launch), \
                 self.assertRaisesRegex(TimeoutError, "PTY fixture deadline expired"):
                capture.main()

            self.assertEqual(terminal.child.returncode, 0)
            self.assertNotIn(b"x" * 64, (output / "terminal.raw").read_bytes())
            with self.assertRaises(ChildProcessError):
                os.waitpid(terminal.child.pid, os.WNOHANG)
            for descriptor in (terminal.master, terminal.slave):
                with self.assertRaises(OSError):
                    os.fstat(descriptor)
            evidence = json.loads((output / "evidence.json").read_text())
            self.assertFalse(evidence["pass"])
            self.assertEqual(evidence["failure"], "TimeoutError")

    def test_recording_failures_preserve_the_capture_error(self):
        for original, blocked in (
            (RuntimeError("capture failed"), ("workflow.cast", "terminal.raw")),
            (KeyboardInterrupt(), ("workflow.cast",)),
            (SystemExit(23), ("evidence.json",)),
        ):
            with self.subTest(error=type(original).__name__, blocked=blocked), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "capture"
                terminal = Mock(events=[], raw=b"partial output")
                terminal.running.return_value = False

                def fail_capture(_marker):
                    for name in blocked:
                        (output / name).mkdir()
                    raise original

                terminal.wait.side_effect = fail_capture
                with patch.object(sys, "argv", ["capture", sys.executable, str(output)]), \
                     patch.object(capture.harness, "Terminal", return_value=terminal), \
                     self.assertRaises(type(original)) as caught:
                    capture.main()

                self.assertIs(caught.exception, original)
                terminal.close.assert_called_once_with()
                if "evidence.json" not in blocked:
                    evidence = json.loads((output / "evidence.json").read_text())
                    self.assertFalse(evidence["pass"])
                    self.assertEqual(evidence["detail"], str(original))
                    for name in blocked:
                        self.assertTrue(any(name in detail for detail in evidence["secondary_failures"]))

    def test_interrupt_during_shutdown_waits_and_closes(self):
        terminal = Mock()
        terminal.child.wait.side_effect = TimeoutError("launcher wait expired")
        interrupted = KeyboardInterrupt()
        with tempfile.TemporaryDirectory() as directory:
            with patch.object(capture, "drain_shutdown_output", side_effect=interrupted), \
                 self.assertRaises(KeyboardInterrupt) as caught:
                capture.finish_capture(terminal, Path(directory), {}, {}, None)
            self.assertIs(caught.exception, interrupted)
            terminal.child.wait.assert_called_once()
            terminal.close.assert_called_once()
            self.assertEqual(list(Path(directory).iterdir()), [])

    def test_second_sigint_waits_for_launcher_and_terminal_close(self):
        events = []

        def interrupt(_signal, _frame):
            events.append("SIGINT")
            raise KeyboardInterrupt

        self.addCleanup(signal.signal, signal.SIGINT, signal.signal(signal.SIGINT, interrupt))
        previous_mask = signal.pthread_sigmask(signal.SIG_BLOCK, set())
        self.addCleanup(signal.pthread_sigmask, signal.SIG_SETMASK, previous_mask)
        initial_mask = (previous_mask | {signal.SIGUSR1}) - {signal.SIGINT}
        signal.pthread_sigmask(signal.SIG_SETMASK, initial_mask)

        def wait_for_launcher(*, timeout):
            signal.raise_signal(signal.SIGINT)
            events.append("wait finished")

        terminal = Mock(events=[], raw=b"")
        terminal.wait.side_effect = lambda _marker: signal.raise_signal(signal.SIGINT)
        terminal.child.wait.side_effect = wait_for_launcher
        terminal.close.side_effect = lambda: events.append("close finished")
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "capture"
            with patch.object(sys, "argv", ["capture", sys.executable, str(output)]), \
                 patch.object(capture.harness, "Terminal", return_value=terminal), \
                 patch.object(capture, "drain_shutdown_output"), self.assertRaises(KeyboardInterrupt):
                capture.main()

        self.assertEqual(events, ["SIGINT", "wait finished", "close finished", "SIGINT"])
        self.assertEqual(signal.pthread_sigmask(signal.SIG_BLOCK, set()), initial_mask)

    def test_sigterm_releases_launcher_during_handoff_and_capture(self):
        original_terminal = capture.harness.Terminal
        original_drain = capture.drain_shutdown_output
        for phase in ("handoff", "capture"):
            with self.subTest(phase=phase), tempfile.TemporaryDirectory() as directory, ExitStack() as cleanup:
                def unexpected_termination(*_):
                    self.fail("SIGTERM reached the caller before capture released the launcher")

                cleanup.callback(signal.signal, signal.SIGTERM, signal.signal(signal.SIGTERM, unexpected_termination))
                old_interrupt = signal.getsignal(signal.SIGINT)
                cleanup.callback(signal.signal, signal.SIGINT, old_interrupt)
                stop_signals = {signal.SIGINT, signal.SIGTERM}
                original_mask = signal.pthread_sigmask(signal.SIG_UNBLOCK, stop_signals)
                cleanup.callback(signal.pthread_sigmask, signal.SIG_SETMASK, original_mask)
                launcher = Path(directory) / "launcher.py"
                launcher.write_text(
                    "import signal, sys\n"
                    "blocked = signal.pthread_sigmask(signal.SIG_BLOCK, set()) & {signal.SIGTERM, signal.SIGINT}\n"
                    "signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))\n"
                    "signal.alarm(15)\n"
                    "print('launcher ready', 'blocked' if blocked else 'unblocked', flush=True)\n"
                    "signal.pause()\n")
                output = Path(directory) / "capture"
                terminal = None

                def launch(*args, **kwargs):
                    nonlocal terminal
                    terminal = original_terminal(*args, **kwargs)
                    cleanup.callback(terminal.close)
                    terminal.wait("launcher ready")
                    self.assertIn(b"launcher ready unblocked", terminal.raw)
                    if phase == "handoff":
                        signal.raise_signal(signal.SIGTERM)
                    else:
                        terminal.wait = lambda _marker: signal.raise_signal(signal.SIGTERM)
                    return terminal

                def drain(owned_terminal, until):
                    if phase == "capture":
                        signal.raise_signal(signal.SIGTERM)
                        signal.raise_signal(signal.SIGINT)
                    original_drain(owned_terminal, until)

                argv = ["capture", sys.executable, str(output), "--launcher", str(launcher)]
                with patch.object(sys, "argv", argv), patch.object(capture.harness, "Terminal", side_effect=launch), \
                     patch.object(capture, "drain_shutdown_output", side_effect=drain) as shutdown, \
                     self.assertRaises(SystemExit) as caught:
                    capture.main()
                self.assertEqual(caught.exception.code, 128 + signal.SIGTERM)
                shutdown.assert_called_once()
                self.assertEqual(terminal.child.returncode, 0)
                with self.assertRaises(ChildProcessError):
                    os.waitpid(terminal.child.pid, os.WNOHANG)
                for descriptor in (terminal.master, terminal.slave):
                    with self.assertRaises(OSError):
                        os.fstat(descriptor)
                self.assertIs(signal.getsignal(signal.SIGTERM), unexpected_termination)
                self.assertIs(signal.getsignal(signal.SIGINT), old_interrupt)
                self.assertEqual(signal.pthread_sigmask(signal.SIG_BLOCK, set()), original_mask - stop_signals)

    def test_failed_launch_replays_pending_signals(self):
        self.addCleanup(signal.signal, signal.SIGTERM, signal.signal(signal.SIGTERM, signal.SIG_IGN))
        self.addCleanup(signal.signal, signal.SIGINT, signal.getsignal(signal.SIGINT))
        original_mask = signal.pthread_sigmask(signal.SIG_UNBLOCK, {signal.SIGINT, signal.SIGTERM})
        self.addCleanup(signal.pthread_sigmask, signal.SIG_SETMASK, original_mask)
        returning_handler = Mock()
        for signals, interrupt_handler, expected in (
            ((signal.SIGTERM,), signal.default_int_handler, SystemExit),
            ((signal.SIGINT,), signal.default_int_handler, KeyboardInterrupt),
            ((signal.SIGINT, signal.SIGTERM), signal.SIG_IGN, SystemExit),
            ((signal.SIGINT, signal.SIGTERM), returning_handler, SystemExit),
        ):
            with self.subTest(signals=signals, interrupt_handler=interrupt_handler), \
                 tempfile.TemporaryDirectory() as directory:
                signal.signal(signal.SIGINT, interrupt_handler)
                launch_error = OSError("could not start launcher")

                def fail_launch(*args, **kwargs):
                    for signum in signals:
                        signal.raise_signal(signum)
                    raise launch_error

                argv = ["capture", sys.executable, str(Path(directory) / "capture")]
                with patch.object(sys, "argv", argv), patch.object(capture.harness, "Terminal", side_effect=fail_launch), \
                     self.assertRaises(expected) as caught:
                    capture.main()
                self.assertIs(caught.exception.__context__, launch_error)
                if expected is SystemExit:
                    self.assertEqual(caught.exception.code, 128 + signal.SIGTERM)
                self.assertIs(signal.getsignal(signal.SIGTERM), signal.SIG_IGN)
                self.assertIs(signal.getsignal(signal.SIGINT), interrupt_handler)
                if interrupt_handler is returning_handler:
                    returning_handler.assert_called_once_with(signal.SIGINT, ANY)


if __name__ == "__main__":
    unittest.main()
