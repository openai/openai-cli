"""Ownership regressions that need no PTY, process launch, or network connection."""

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


if __name__ == "__main__":
    unittest.main()
