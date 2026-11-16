from contextlib import ExitStack
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

import try_browser


class BrowserLauncherTest(unittest.TestCase):
    def test_launcher_always_uses_its_loopback_service(self):
        terminal = Mock()
        terminal.isatty.return_value = True
        with tempfile.TemporaryDirectory() as directory, ExitStack() as cleanup:
            child = Mock()
            child.poll.return_value = child.returncode = 0
            for sig in (signal.SIGTERM, signal.SIGINT):
                cleanup.callback(signal.signal, sig, signal.getsignal(sig))
            with patch.dict(os.environ, {"OPENAI_API_KEY": "unused-fake-key", "OPENAI_ADMIN_KEY": "unused-fake-admin",
                                         "OPENAI_BASE_URL": "https://unused.invalid", "HTTP_PROXY": "http://unused.invalid"}), \
                 patch.multiple(sys, stdin=terminal, stdout=terminal, stderr=terminal,
                                argv=["try_browser.py", sys.executable]), \
                 patch.object(tempfile, "mkdtemp", return_value=directory), \
                 patch.object(subprocess, "Popen", return_value=child) as launch:
                self.assertEqual(try_browser.main(), 0)
            env = launch.call_args.kwargs["env"]
            self.assertEqual(env["OPENAI_API_KEY"], "synthetic-demo-key")
            self.assertEqual(env["OPENAI_PROJECT_ID"], "proj_synthetic_local_demo")
            self.assertRegex(env["OPENAI_BASE_URL"], r"^http://127\.0\.0\.1:[0-9]+/v1$")
            self.assertEqual(env["HOME"], str(Path(directory) / "home"))
            self.assertEqual(env["OPENAI_CLI_NO_UPDATE_CHECK"], "1")
            self.assertNotIn("OPENAI_ADMIN_KEY", env)
            self.assertNotIn("HTTP_PROXY", env)
            examples = launch.call_args.kwargs["cwd"]
            self.assertEqual(sorted(p.name for p in examples.iterdir()), ["requests.jsonl", "tickets.csv"])
            self.assertEqual((examples / "tickets.csv").read_bytes(), try_browser.TICKETS.read_bytes())

    def test_ticket_fixture_matches_uploaded_ids_and_reverses_results(self):
        records = [
            {"custom_id": identifier, "method": "POST", "url": "/v1/responses", "body": {
                "input": message, "model": "synthetic-demo-model", "instructions": "Classify each ticket."
            }} for identifier, message in zip(("batch-row-a", "batch-row-b", "batch-row-c"), try_browser.TICKET_CATEGORIES)
        ]

        def encode(rows):
            return b"".join((json.dumps(row) + "\n").encode() for row in rows)

        answers = [json.loads(line) for line in try_browser.ticket_results(encode(records)).splitlines()]
        self.assertEqual([record["custom_id"] for record in answers], ["batch-row-c", "batch-row-b", "batch-row-a"])
        self.assertEqual([record["response"]["body"]["output"][0]["content"][0]["text"] for record in answers],
                         ["other", "login", "billing"])
        for invalid in (records[:-1], [records[0], records[0], records[2]],
                        [*records[:2], dict(records[2], custom_id=records[1]["custom_id"])],
                        [*records[:2], dict(records[2], body=dict(records[2]["body"], input="unknown sample"))]):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                try_browser.ticket_results(encode(invalid))

    def test_stop_during_child_handoff(self):
        for sig in (signal.SIGTERM, signal.SIGINT):
            self.addCleanup(signal.signal, sig, signal.getsignal(sig))
        popen = subprocess.Popen
        terminal = Mock()
        terminal.isatty.return_value = True
        for first, second in ((signal.SIGTERM, signal.SIGINT), (signal.SIGINT, signal.SIGTERM)):
            with self.subTest(first=first), tempfile.TemporaryDirectory() as directory, ExitStack() as cleanup:
                child = None

                def spawn(*_args, **kwargs):
                    nonlocal child
                    child = popen([sys.executable, "-c", "import time; time.sleep(8)"], **kwargs)
                    cleanup.callback(try_browser.stop_process, child)
                    # Deliver both signals after spawning, before main receives its child.
                    for sig in (first, second):
                        handler = signal.getsignal(sig)
                        self.assertTrue(callable(handler) and handler != signal.default_int_handler)
                        signal.raise_signal(sig)
                    return child

                with patch.multiple(sys, stdin=terminal, stdout=terminal, stderr=terminal,
                                    argv=["try_browser.py", sys.executable]), \
                     patch.object(tempfile, "mkdtemp", return_value=directory), \
                     patch.object(subprocess, "Popen", side_effect=spawn):
                    self.assertEqual(try_browser.main(), 128 + first)

                self.assertIsNotNone(child.returncode)
                self.assertNotEqual(child.returncode, 0)
                receipt = json.loads((Path(directory) / "session.json").read_text())
                self.assertEqual(receipt["exit"], 128 + first)
                self.assertTrue(receipt["process_stopped"])
                self.assertTrue(receipt["server_stopped"])


if __name__ == "__main__":
    unittest.main()
