from contextlib import ExitStack
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

import try_browser


class BrowserLauncherTest(unittest.TestCase):
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
