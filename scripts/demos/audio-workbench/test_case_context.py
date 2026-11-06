"""Ownership and capture regressions without PTYs, process launches, or network connections."""

from contextlib import contextmanager
import importlib.util
import json
from pathlib import Path
import signal
import tempfile
import threading
from types import SimpleNamespace
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("audio_case_context", Path(__file__).with_name("case_context.py"))
context = importlib.util.module_from_spec(spec)
spec.loader.exec_module(context)

spec = importlib.util.spec_from_file_location("audio_shared_terminal", Path(__file__).resolve().parents[2] / "image_picker_harness.py")
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)


class Child:
    pid = 987654
    returncode = 0

    def poll(self):
        return self.returncode

    def wait(self, timeout):
        return self.returncode


class ParentTerminal:
    def __init__(self, *arguments, **options):
        self.child = Child()
        self.initial_size = (options["width"], options["height"])
        self.closed = False

    def close(self):
        self.closed = True


class Fixture:
    def __init__(self, **options):
        self.partial_release = threading.Event()
        self.lock = threading.Lock()
        self.handler_threads = set()
        self.closed = False

    def server_close(self):
        self.closed = True


class OwnershipTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        binary = self.directory / "unused-binary"
        binary.write_bytes(b"Synthetic identity only; never executed")
        args = SimpleNamespace(binary=binary, output=self.directory, source="0" * 40,
                               theme="dark", emit=False)
        self.run = context.Harness(args, SimpleNamespace(Terminal=ParentTerminal),
                                   SimpleNamespace(SpeechFixture=Fixture))

    def test_observer_failure_registers_terminal_with_its_explicit_case(self):
        first = context.CaseContext(self.run, "navigation")
        second = context.CaseContext(self.run, "failure")
        self.run.observer_factory = lambda *size: (_ for _ in ()).throw(ValueError("observer startup"))
        with self.assertRaisesRegex(ValueError, "observer startup"):
            first.new_terminal("unused", [], {}, width=90, height=34)
        self.assertEqual(second.terminals, [])
        self.assertEqual(len(first.terminals), 1)
        terminal = first.terminals[0]
        self.assertIs(terminal.case_result, first.result)
        self.assertEqual(terminal.cleanup["case"], "navigation")
        with patch.object(context, "group_exists", return_value=False):
            first.close()
        self.assertTrue(terminal.closed)
        self.assertTrue(terminal.cleanup["group_absent_verified"])

    def test_fixture_registration_survives_thread_constructor_failure(self):
        case = context.CaseContext(self.run, "startup")
        server = Fixture()
        self.run.fixture_class = lambda **options: server
        with patch.object(context.threading, "Thread", side_effect=RuntimeError("thread constructor")):
            with self.assertRaisesRegex(RuntimeError, "thread constructor"):
                case.execute(lambda _: self.fail("Scenario must not start"))
        self.assertIs(case.server_state["server"], server)
        self.assertIsNone(case.server_state["worker"])
        self.assertTrue(server.closed)
        self.assertTrue(case.server_state["closed"])
        self.assertFalse(json.loads((self.directory / "results.json").read_text())["cases"][0]["pass"])

    def test_decoder_failure_keeps_terminal_registered(self):
        case = context.CaseContext(self.run, "startup")
        with patch.object(context.codecs, "getincrementaldecoder", side_effect=RuntimeError("decoder startup")):
            with self.assertRaisesRegex(RuntimeError, "decoder startup"):
                case.new_terminal("unused", [], {}, width=90, height=34)
        self.assertEqual(len(case.terminals), 1)
        with patch.object(context, "group_exists", return_value=False):
            case.close()
        self.assertTrue(case.terminals[0].closed)

    def test_fixture_registration_survives_thread_start_failure(self):
        case = context.CaseContext(self.run, "startup")
        server = Fixture()
        self.run.fixture_class = lambda **options: server
        worker = SimpleNamespace(ident=None, is_alive=lambda: False)
        worker.start = lambda: (_ for _ in ()).throw(RuntimeError("thread start"))
        with patch.object(context.threading, "Thread", return_value=worker):
            with self.assertRaisesRegex(RuntimeError, "thread start"):
                case.execute(lambda _: self.fail("Scenario must not start"))
        self.assertIs(case.server_state["worker"], worker)
        self.assertTrue(server.closed)
        self.assertTrue(case.result["server_cleanup"]["serve_thread_joined"])

    def test_interruption_before_terminal_return_keeps_ownership(self):
        run = self.run

        class InterruptedTerminal(ParentTerminal):
            def __init__(self, *arguments, **options):
                super().__init__(*arguments, **options)
                run.interrupt_harness(signal.SIGTERM, None)

        run.terminal_class = context.terminal_class(SimpleNamespace(Terminal=InterruptedTerminal))
        case = context.CaseContext(run, "startup")
        with self.assertRaises(context.HarnessInterrupted):
            case.new_terminal("unused", [], {}, width=90, height=34)
        self.assertEqual(len(case.terminals), 1)
        run.interruption["finalizing"] = True
        with patch.object(context, "group_exists", return_value=False):
            case.close()
        self.assertTrue(case.terminals[0].closed)
        self.assertFalse(case.result["pass"])

    def test_cleanup_attempts_every_owned_terminal_after_one_failure(self):
        case = context.CaseContext(self.run, "startup")
        closed = []

        def fail():
            closed.append("first")
            raise RuntimeError("first cleanup")

        case.terminals = [SimpleNamespace(close=fail), SimpleNamespace(close=lambda: closed.append("second"))]
        with self.assertRaisesRegex(RuntimeError, "first cleanup"):
            case.close()
        self.assertEqual(closed, ["first", "second"])
        self.assertFalse(case.result["pass"])


class CaptureTests(unittest.TestCase):
    reset = b"\x1b[?2004l\x1b[?25h"

    @contextmanager
    def capture(self, chunks, *, exit_status=130, repeating=False):
        terminal_type = context.terminal_class(picker)
        terminal = terminal_type.__new__(terminal_type)
        terminal.child = Child()
        terminal.child.returncode = exit_status
        terminal.master, terminal.slave = -100, -101  # Patched calls never open these descriptors.
        terminal.initial = [0, 0, 0, 0]
        terminal.raw = bytearray(b"\x1b[?25l\x1b[?2004h")
        terminal.events, terminal.started = [], 0
        terminal.decoder = context.codecs.getincrementaldecoder("utf-8")("replace")
        terminal.decoder_finished = False
        observed = bytearray()
        terminal.navigation_observer = SimpleNamespace(feed=observed.extend)
        terminal.context = SimpleNamespace(args=SimpleNamespace(theme="dark", emit=False))
        pending, clock = list(chunks), [0.0]

        def ready(readers, writers, errors, duration):
            clock[0] += min(duration, .01) if pending else duration
            return (readers if pending else [], [], [])

        def read(_descriptor, _length):
            return pending[0] if repeating else pending.pop(0)

        with patch.object(context.time, "monotonic", side_effect=lambda: clock[0]), \
                patch.object(context.select, "select", side_effect=ready), \
                patch.object(context.os, "read", side_effect=read), \
                patch.object(picker.termios, "tcgetattr", return_value=[0, 0, 0, 0]):
            yield terminal, pending, observed, clock

    def test_finish_drains_all_post_exit_chunks_before_shared_assertions(self):
        chunks = [b"first", b"second", b"third", b"fourth", self.reset]
        with self.capture(chunks) as (terminal, pending, observed, _clock):
            with self.assertRaisesRegex(AssertionError, "bracketed paste was not restored"):
                picker.Terminal.finish(terminal, 130)
            self.assertEqual(pending, chunks[3:])
            self.assertEqual(observed, b"".join(chunks[:3]))
        with self.capture(chunks) as (terminal, pending, observed, _clock):
            terminal.finish(130)
            self.assertEqual(pending, [])
            self.assertEqual(observed, b"".join(chunks))
            self.assertTrue(terminal.raw.endswith(b"".join(chunks)))
            self.assertEqual("".join(event[2] for event in terminal.events).encode(), b"".join(chunks))
            self.assertIsNone(terminal.finish_deadline)

    def test_finish_preserves_wrong_exit_failure(self):
        with self.capture([self.reset], exit_status=1) as (terminal, _pending, _observed, _clock):
            with self.assertRaises(AssertionError) as caught:
                terminal.finish(130)
            self.assertEqual(caught.exception.args[0][:2], (1, 130))
            self.assertIsNone(terminal.finish_deadline)

    def test_finish_preserves_missing_restoration_failure(self):
        with self.capture([b"completed output"]) as (terminal, _pending, _observed, _clock):
            with self.assertRaisesRegex(AssertionError, "bracketed paste was not restored"):
                terminal.finish(130)
            self.assertIsNone(terminal.finish_deadline)

    def test_finish_bounds_live_exit_and_post_exit_drain(self):
        for exit_status in (None, 130):
            with self.subTest(exit_status=exit_status):
                with self.capture([b"still writing"], exit_status=exit_status, repeating=True) as capture:
                    terminal, _pending, _observed, clock = capture
                    with self.assertRaisesRegex(AssertionError, "exit deadline"):
                        terminal.finish(130, timeout=.2)
                    self.assertLessEqual(clock[0], .2)
                    self.assertIsNone(terminal.finish_deadline)


if __name__ == "__main__":
    unittest.main()
