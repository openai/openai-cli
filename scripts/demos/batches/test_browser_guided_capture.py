import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch

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
        for blocked in (("workflow.cast", "terminal.raw"), ("evidence.json",)):
            with self.subTest(blocked=blocked), tempfile.TemporaryDirectory() as directory:
                output = Path(directory) / "capture"
                original = RuntimeError("capture failed")
                terminal = Mock(events=[], raw=b"partial output")
                terminal.running.return_value = False

                def fail_capture(_marker):
                    for name in blocked:
                        (output / name).mkdir()
                    raise original

                terminal.wait.side_effect = fail_capture
                with patch.object(sys, "argv", ["capture", sys.executable, str(output)]), \
                     patch.object(capture.harness, "Terminal", return_value=terminal), \
                     self.assertRaises(RuntimeError) as caught:
                    capture.main()

                self.assertIs(caught.exception, original)
                terminal.close.assert_called_once_with()
                if "evidence.json" not in blocked:
                    evidence = json.loads((output / "evidence.json").read_text())
                    self.assertFalse(evidence["pass"])
                    self.assertEqual(evidence["detail"], str(original))
                    for name in blocked:
                        self.assertTrue(any(name in detail for detail in evidence["secondary_failures"]))


if __name__ == "__main__":
    unittest.main()
