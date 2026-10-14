#!/usr/bin/env python3
"""Offline controls for the WSL pager pilot. Never imports or starts a distro."""

from datetime import datetime, timedelta, timezone
import importlib.util
import io
import os
from pathlib import Path
import socket
import ssl
import sys
import tempfile
import time
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location("wsl_pager", Path(__file__).with_name("check-wsl-pager.py"))
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)


def passing_output():
    root = pilot.ROOT
    return (f"=== RUN   {root}\n=== RUN   {root}/LookPath\n"
            f"=== RUN   {root}/ForkExec\n--- PASS: {root} (0.02s)\n"
            f"    --- PASS: {root}/LookPath (0.01s)\n"
            f"    --- PASS: {root}/ForkExec (0.01s)\nPASS\n").encode()


def process_passing_output():
    names = sorted(pilot.PROCESS_TESTS)
    return ("".join(f"=== RUN   {name}\n--- PASS: {name} (0.01s)\n" for name in names) + "PASS\n").encode()


class MetadataTests(unittest.TestCase):
    def test_enabled_feature(self):
        report = ("Deployment Image Servicing and Management tool\nVersion: synthetic\n\n"
                  "Feature Name : Microsoft-Windows-Subsystem-Linux\nDisplay Name : Windows Subsystem for Linux\n"
                  "State : Enabled\nRestart Required : Possible\n\nThe operation completed successfully.\n")
        for encoding in ("utf-8", "utf-8-sig", "utf-16", "utf-16-le"):
            for newline in ("\n", "\r\n"):
                with self.subTest(encoding=encoding, newline=newline):
                    pilot.require_feature_enabled(report.replace("\n", newline).encode(encoding), b"")
        pilot.require_feature_enabled(b"\tFeature Name\t:\tMicrosoft-Windows-Subsystem-Linux \r\n  State : Enabled\t\r\n", b"")

    def test_unverified_feature(self):
        name = "Feature Name : Microsoft-Windows-Subsystem-Linux\n"
        enabled = "State : Enabled\n"
        bad = {"missing": "", "name only": name, "state only": enabled,
               "duplicate name": name + name + enabled, "duplicate state": name + enabled + enabled,
               "conflicting states": name + enabled + "State : Disabled\n",
               "wrong name": "Feature Name : VirtualMachinePlatform\n" + enabled,
               "name suffix": name.rstrip() + " extra\n" + enabled,
               "name prefix": "Feature Name : Other-Microsoft-Windows-Subsystem-Linux\n" + enabled,
               "label prefix": "Not " + name + enabled,
               "value on another line": name + "State :\nEnabled\n"}
        for value in ("Disabled", "Absent", "Unknown", "Enable Pending", "Disable Pending",
                      "Disabled with Payload Removed", "Enabled Pending", "Not Enabled", "Enabled extra",
                      "Enabled; Disabled", "Enabled\tunexpected", "1", "True", ""):
            bad["state " + value] = name + "State : " + value + "\n"
        for label, report in bad.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(RuntimeError, "not verified as already enabled"):
                    pilot.require_feature_enabled(report.encode(), b"")

    def test_malformed_feature(self):
        for raw in (b"\xff", b"a\x00b", b"Feature Name : Microsoft-Windows-Subsystem-Linux\nState : Enabled\x1b[0m\n"):
            with self.subTest(raw=raw):
                with self.assertRaisesRegex(RuntimeError, "encoding|metadata controls"):
                    pilot.require_feature_enabled(raw, b"")

    def test_feature_stderr_stays_failure(self):
        data = b"Feature Name : Microsoft-Windows-Subsystem-Linux\nState : Enabled\n"
        with self.assertRaisesRegex(RuntimeError, "wrote stderr"):
            pilot.require_feature_enabled(data, b"query failed")

    def test_native_process_identity(self):
        with patch.object(pilot.sys, "platform", "win32"), patch.object(pilot.platform, "machine", return_value="AMD64"), \
                patch.object(pilot.struct, "calcsize", return_value=8):
            identity = pilot.require_native_windows_process()
            self.assertEqual(identity["pointer_bits"], 64)
            self.assertEqual(identity["pid"], os.getpid())
            self.assertEqual(identity["python_executable"], sys.executable)
        for system, machine, pointer_bytes in (("linux", "x86_64", 8), ("win32", "ARM64", 8), ("win32", "AMD64", 4)):
            with self.subTest(system=system, machine=machine, pointer_bytes=pointer_bytes):
                with patch.object(pilot.sys, "platform", system), patch.object(pilot.platform, "machine", return_value=machine), \
                        patch.object(pilot.struct, "calcsize", return_value=pointer_bytes):
                    with self.assertRaisesRegex(RuntimeError, "64-bit Python process on Windows AMD64"):
                        pilot.require_native_windows_process()

    def test_inventory_encodings(self):
        for encoding in ("utf-8", "utf-8-sig", "utf-16", "utf-16-le"):
            with self.subTest(encoding=encoding):
                raw = "  owned-123\r\n".encode(encoding)
                self.assertEqual(pilot.distribution_names(raw), ["owned-123"])

    def test_version_one(self):
        for encoding in ("utf-8", "utf-16-le"):
            with self.subTest(encoding=encoding):
                pilot.require_version_one("  NAME STATE VERSION\r\n* owned-123 Stopped 1\r\n".encode(encoding), "owned-123")

    def test_wrong_version_and_identity(self):
        for raw in (b"owned-123 Stopped 2\n", b"other Stopped 1\n", b"owned-123 Stopped 1\nowned-123 Running 1\n"):
            with self.subTest(raw=raw):
                with self.assertRaisesRegex(RuntimeError, "not verified as WSL 1"):
                    pilot.require_version_one(raw, "owned-123")

    def test_malformed_inventory(self):
        with self.assertRaises(UnicodeDecodeError):
            pilot.distribution_names(b"a\x00b")
        with self.assertRaisesRegex(RuntimeError, "metadata controls"):
            pilot.distribution_names(b"owned\x1b[0m\n")
        with self.assertRaisesRegex(RuntimeError, "Duplicate"):
            pilot.distribution_names(b"owned\nowned\n")


class AcceptanceTests(unittest.TestCase):
    def test_exact_pass(self):
        pilot.require_test_pass(passing_output(), b"", 0)

    def test_incomplete_or_false_passes(self):
        good = passing_output()
        bad = {
            "zero tests": b"PASS\n",
            "missing child": good.replace(f"=== RUN   {pilot.ROOT}/ForkExec\n".encode(), b""),
            "duplicate root": good + f"=== RUN   {pilot.ROOT}\n".encode(),
            "skip": good + f"--- SKIP: {pilot.ROOT}/other (0.0s)\n".encode(),
            "fail": good + f"--- FAIL: {pilot.ROOT}/other (0.0s)\n".encode(),
            "panic": good + b"panic: unexpected\n",
            "extra test": good + b"=== RUN   TestOther\n--- PASS: TestOther (0.0s)\n",
            "wrong test": good.replace(pilot.ROOT.encode(), b"TestDifferent"),
            "no conclusion": good.removesuffix(b"PASS\n"),
        }
        for name, raw in bad.items():
            with self.subTest(name=name):
                with self.assertRaisesRegex(RuntimeError, "pager test execution"):
                    pilot.require_test_pass(raw, b"", 0)

    def test_exit_and_stderr_are_independent(self):
        for stderr, code in ((b"", 7), (b"unexpected diagnostic\n", 0)):
            with self.subTest(code=code, stderr=stderr):
                with self.assertRaisesRegex(RuntimeError, "failed or wrote stderr"):
                    pilot.require_test_pass(passing_output(), stderr, code)

    def test_ubuntu_output_is_not_metadata(self):
        with self.assertRaises(UnicodeDecodeError):
            pilot.require_test_pass(b"\xff\xfe" + passing_output().decode().encode("utf-16-le"), b"", 0)

    def test_process_exact_inventory(self):
        self.assertEqual(len(pilot.PROCESS_CASES), 23)
        self.assertEqual(len(pilot.PROCESS_TESTS), 144)
        self.assertNotIn("TestMainDispatchProcess", pilot.PROCESS_TESTS)
        pilot.require_test_pass(process_passing_output(), b"", 0, pilot.PROCESS_TESTS, "process")

    def test_process_requires_saved_image_cases(self):
        good = process_passing_output()
        for name in (
            "TestMainImageGenerationPartialFailureKeepsSavedImage",
            "TestMainImageGenerationStreamSavesOnlyFinalAndClosesResponse",
            "TestMainImageGenerationClosedStdoutKeepsSavedImage",
            "TestMainImageGenerationClosedStdoutKeepsSavedImage/partial-failure=true/format=json",
            "TestMainImageEditStreamSavesFinalAndCloses",
            "TestMainImageUploadBatchCollisionAndPartialFailure",
            "TestMainImageEditInterruptClosesStreamWithoutSavingPartial",
        ):
            with self.subTest(name=name):
                self.assertIn(name, pilot.PROCESS_TESTS)
                missing = good.replace(f"=== RUN   {name}\n".encode(), b"").replace(
                    f"--- PASS: {name} (0.01s)\n".encode(), b"")
                with self.assertRaisesRegex(RuntimeError, "process test execution"):
                    pilot.require_test_pass(missing, b"", 0, pilot.PROCESS_TESTS, "process")

    def test_process_rejects_missing_duplicate_and_unexpected_events(self):
        good = process_passing_output()
        name = "TestMainFilesWorkflowExplicitBinaryStdin/JSON_stays_file_bytes"
        run = f"=== RUN   {name}\n".encode()
        passed = f"--- PASS: {name} (0.01s)\n".encode()
        bad = {"missing run": good.replace(run, b""), "missing pass": good.replace(passed, b""),
               "duplicate run": good + run, "duplicate pass": good + passed,
               "unexpected": good + b"=== RUN   TestOther\n--- PASS: TestOther (0.01s)\n",
               "skipped": good + b"--- SKIP: TestOther (0.01s)\n",
               "failed": good + b"--- FAIL: TestOther (0.01s)\n",
               "no final pass": good.removesuffix(b"PASS\n"), "duplicate final pass": good + b"PASS\n",
               "pager instead": passing_output(), "empty": b"PASS\n"}
        for label, raw in bad.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(RuntimeError, "process test execution"):
                    pilot.require_test_pass(raw, b"", 0, pilot.PROCESS_TESTS, "process")

    def test_process_preserves_exit_stderr_and_encoding_gates(self):
        for stderr, code in ((b"", 7), (b"diagnostic", 0)):
            with self.subTest(code=code, stderr=stderr):
                with self.assertRaisesRegex(RuntimeError, "process test failed or wrote stderr"):
                    pilot.require_test_pass(process_passing_output(), stderr, code, pilot.PROCESS_TESTS, "process")
        with self.assertRaises(UnicodeDecodeError):
            pilot.require_test_pass(b"\xff" + process_passing_output(), b"", 0, pilot.PROCESS_TESTS, "process")

    def test_each_payload_uses_its_own_destination_and_hash(self):
        class FakeCommands:
            def __init__(self, observed):
                self.observed, self.calls = observed, []

            def run(self, label, argv, seconds):
                self.calls.append((label, argv, seconds))
                value = b"/mnt/c/synthetic test\n" if label.endswith("binary-path") else self.observed
                return value, b"", 0

        for prefix, target, expected in (("", "/home/cli-compat/payload/custom.test", "pager-digest"),
                                         ("process-", "/home/cli-compat/payload/process.test", "process-digest")):
            with self.subTest(prefix=prefix):
                commands = FakeCommands((expected + "  " + target + "\n").encode())
                pilot.copy_payload(commands, ["root"], ["user"], Path("C:/synthetic test"), target, expected, prefix)
                self.assertEqual([x[0] for x in commands.calls],
                                 [prefix + "binary-path", prefix + "copy", prefix + "binary-hash"])
                self.assertEqual(commands.calls[1][1], ["root", "/usr/bin/install", "-m", "700", "-o", "cli-compat", "-g", "cli-compat", "--", "/mnt/c/synthetic test", target])
                self.assertEqual(commands.calls[2][1], ["user", "/usr/bin/sha256sum", target])
                with self.assertRaisesRegex(RuntimeError, "copied Linux binary changed"):
                    pilot.copy_payload(FakeCommands(b"wrong  target\n"), ["root"], ["user"], Path("test"), target, expected, prefix)

    def test_process_stage_requires_full_admission_and_fixed_command(self):
        class FakeCommands:
            deadline = 200.0

            def __init__(self):
                self.calls = []

            def run(self, label, argv, seconds):
                self.calls.append((label, argv, seconds))
                return process_passing_output(), b"", 0

        commands = FakeCommands()
        with patch.object(pilot.time, "monotonic", return_value=30.001):
            with self.assertRaisesRegex(RuntimeError, "Insufficient work time"):
                pilot.run_process_suite(commands, ["user"], "/process.test")
        self.assertEqual(commands.calls, [])
        with patch.object(pilot.time, "monotonic", return_value=30.0):
            pilot.run_process_suite(commands, ["user"], "/process.test")
        self.assertEqual(commands.calls, [("process", ["user", "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s", "/process.test", "-test.run=" + pilot.PROCESS_SELECTOR, "-test.timeout=150s", "-test.v"], 170)])

    def test_process_stage_does_not_accept_a_false_command_result(self):
        class FakeCommands:
            deadline = 200.0

            def __init__(self, result):
                self.result = result

            def run(self, *_args):
                return self.result

        with patch.object(pilot.time, "monotonic", return_value=0):
            for result in ((process_passing_output(), b"", 1),
                           (process_passing_output(), b"unexpected", 0),
                           (b"PASS\n", b"", 0)):
                with self.subTest(code=result[2], stderr=result[1]):
                    with self.assertRaises(RuntimeError):
                        pilot.run_process_suite(FakeCommands(result), ["user"], "/process.test")

    def test_binary_architecture(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "test"
            header = b"\x7fELF\x02\x01" + b"\0" * 12 + b"\x3e\x00"
            path.write_bytes(header)
            self.assertEqual(pilot.require_linux_binary(path), pilot.digest(path))
            for data in (b"MZ" + header, header[:18] + b"\xb7\x00", header[:4] + b"\x01" + header[5:]):
                path.write_bytes(data)
                with self.assertRaisesRegex(RuntimeError, "AMD64 ELF"):
                    pilot.require_linux_binary(path)

    def test_image_download_checks_actual_bytes(self):
        class Response(io.BytesIO):
            headers = {}

        class Opener:
            def open(self, *_args, **_kwargs):
                return Response(b"synthetic image")

        with tempfile.TemporaryDirectory() as directory:
            with patch.object(pilot.urllib.request, "build_opener", return_value=Opener()):
                with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                    pilot.download_image(Path(directory) / "wrong-image", time.monotonic() + 5)
                with patch.object(pilot, "IMAGE_LIMIT", 3):
                    with self.assertRaisesRegex(RuntimeError, "download limit"):
                        pilot.download_image(Path(directory) / "large-image", time.monotonic() + 5)
                with patch.object(pilot, "IMAGE_SHA256", pilot.hashlib.sha256(b"synthetic image").hexdigest()):
                    self.assertEqual(pilot.download_image(Path(directory) / "good-image", time.monotonic() + 5), 15)

    def test_late_download_eof_is_failure(self):
        clock = [0.0]

        class Response(io.BytesIO):
            headers = {}

            def read1(self, _size):
                clock[0] = 2.0
                return b""

        class Opener:
            def open(self, *_args, **_kwargs):
                return Response()

        with tempfile.TemporaryDirectory() as directory:
            with patch.object(pilot.urllib.request, "build_opener", return_value=Opener()), \
                    patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]), \
                    patch.object(pilot, "IMAGE_SHA256", pilot.hashlib.sha256(b"").hexdigest()):
                with self.assertRaisesRegex(RuntimeError, "timed out"):
                    pilot.download_image(Path(directory) / "late-image", 1.0)

    def test_late_download_close_is_failure(self):
        clock = [0.0]

        class Response(io.BytesIO):
            headers = {}

            def __exit__(self, *args):
                clock[0] = 2.0
                return super().__exit__(*args)

        class Opener:
            def open(self, *_args, **_kwargs):
                return Response()

        with tempfile.TemporaryDirectory() as directory:
            with patch.object(pilot.urllib.request, "build_opener", return_value=Opener()), \
                    patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]), \
                    patch.object(pilot, "IMAGE_SHA256", pilot.hashlib.sha256(b"").hexdigest()):
                with self.assertRaisesRegex(RuntimeError, "timed out"):
                    pilot.download_image(Path(directory) / "late-close", 1.0)


class DownloadRetryTests(unittest.TestCase):
    class Response(io.BytesIO):
        headers = {}

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "ubuntu.wsl"
        self.addCleanup(setattr, pilot, "STOP", 0)

    def test_startup_timeout_retries_once_before_creating_image(self):
        data = b"synthetic image"
        for failure in (TimeoutError("synthetic timeout"), pilot.urllib.error.URLError(TimeoutError("synthetic timeout"))):
            with self.subTest(type=type(failure).__name__):
                response = self.Response(data)
                opener = Mock()
                opener.open.side_effect = [failure, response]
                attempts = []
                with patch.object(pilot.urllib.request, "build_opener", return_value=opener), \
                        patch.object(pilot.time, "sleep") as sleep, \
                        patch.object(pilot, "IMAGE_SHA256", pilot.hashlib.sha256(data).hexdigest()):
                    self.assertEqual(pilot.download_image(self.path, time.monotonic() + 100, attempts), len(data))
                self.assertEqual(self.path.read_bytes(), data)
                self.path.unlink()
                self.assertTrue(response.closed)
                self.assertEqual(opener.open.call_count, 2)
                for call in opener.open.call_args_list:
                    self.assertEqual(call.args, (pilot.IMAGE_URL,))
                    self.assertEqual(call.kwargs, {"timeout": 10})
                sleep.assert_called_once_with(1)
                self.assertEqual([a["outcome"] for a in attempts], ["failed", "opened"])
                self.assertEqual(attempts[0]["reason_type"], "TimeoutError")

    def test_repeated_startup_timeout_preserves_final_failure_without_image(self):
        final = pilot.urllib.error.URLError(TimeoutError("second timeout"))
        opener = Mock()
        opener.open.side_effect = [TimeoutError("first timeout"), final]
        attempts = []
        with patch.object(pilot.urllib.request, "build_opener", return_value=opener), patch.object(pilot.time, "sleep"):
            with self.assertRaises(pilot.urllib.error.URLError) as caught:
                pilot.download_image(self.path, time.monotonic() + 100, attempts)
        self.assertIs(caught.exception, final)
        self.assertEqual(opener.open.call_count, 2)
        self.assertEqual([a["outcome"] for a in attempts], ["failed", "failed"])
        self.assertFalse(self.path.exists())

    def test_other_startup_failures_never_retry(self):
        failures = [ssl.SSLCertVerificationError(1, "synthetic certificate failure"),
                    pilot.urllib.error.URLError(ssl.SSLCertVerificationError(1, "synthetic certificate failure")),
                    ssl.SSLError(1, "synthetic TLS failure"),
                    pilot.urllib.error.HTTPError(pilot.IMAGE_URL, 503, "timeout-looking text", None, None),
                    pilot.urllib.error.HTTPError(pilot.IMAGE_URL, 503, TimeoutError("synthetic HTTP reason"), None, None),
                    pilot.urllib.error.URLError(socket.gaierror(-2, "synthetic DNS failure")),
                    ConnectionResetError("synthetic reset"),
                    pilot.urllib.error.URLError("The handshake operation timed out")]
        for failure in failures:
            with self.subTest(type=type(failure).__name__, message=str(failure)):
                opener = Mock()
                opener.open.side_effect = failure
                attempts = []
                with patch.object(pilot.time, "sleep") as sleep:
                    with self.assertRaises(type(failure)) as caught:
                        pilot.open_image(opener, time.monotonic() + 100, attempts)
                self.assertIs(caught.exception, failure)
                opener.open.assert_called_once()
                sleep.assert_not_called()
                self.assertEqual(len(attempts), 1)
                self.assertFalse(self.path.exists())

    def test_body_timeout_never_retries_and_owned_partial_file_is_removed(self):
        class Partial(self.Response):
            reads = 0

            def read1(self, _size):
                self.reads += 1
                if self.reads == 1:
                    return b"partial"
                raise TimeoutError("synthetic body timeout")

        response = Partial()
        opener = Mock()
        opener.open.return_value = response
        with patch.object(pilot.urllib.request, "build_opener", return_value=opener):
            with self.assertRaisesRegex(TimeoutError, "synthetic body timeout"):
                pilot.download_image(self.path, time.monotonic() + 100)
        opener.open.assert_called_once()
        self.assertTrue(response.closed)
        self.assertEqual(self.path.read_bytes(), b"partial")
        commands = Mock()
        commands.run.side_effect = AssertionError("No WSL command is needed before import")
        pilot.clean_distro(commands, "wsl.exe", "owned", {"import_attempted": False, "directory": self.directory.name})
        self.assertFalse(self.path.exists())

    def test_integrity_and_file_failures_never_retry(self):
        class WriteFailure(io.BytesIO):
            def write(self, _data):
                raise OSError("synthetic write failure")

        for phase in ("checksum", "existing file", "write"):
            with self.subTest(phase=phase):
                response = self.Response(b"synthetic")
                opener = Mock()
                opener.open.return_value = response
                with patch.object(pilot.urllib.request, "build_opener", return_value=opener):
                    if phase == "write":
                        output = WriteFailure()
                        with patch.object(Path, "open", return_value=output):
                            with self.assertRaisesRegex(OSError, "synthetic write failure"):
                                pilot.download_image(self.path, time.monotonic() + 100)
                        self.assertTrue(output.closed)
                    elif phase == "existing file":
                        self.path.write_bytes(b"preserved")
                        with self.assertRaises(FileExistsError):
                            pilot.download_image(self.path, time.monotonic() + 100)
                        self.assertEqual(self.path.read_bytes(), b"preserved")
                        self.path.unlink()
                    else:
                        with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                            pilot.download_image(self.path, time.monotonic() + 100)
                        self.path.unlink()
                opener.open.assert_called_once()
                self.assertTrue(response.closed)

    def test_stop_and_remaining_budget_prevent_second_attempt(self):
        for boundary in ("before delay", "after delay", "stop"):
            with self.subTest(boundary=boundary):
                pilot.STOP = 0
                clock = [0.0]
                failure = TimeoutError("first timeout")
                opener = Mock()
                opener.open.side_effect = failure

                def delay(_seconds):
                    clock[0] = 91.0
                    if boundary == "stop":
                        pilot.STOP = 15

                deadline = 10.99 if boundary == "before delay" else 100.0
                with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]), \
                        patch.object(pilot.time, "sleep", side_effect=delay):
                    with self.assertRaises(TimeoutError) as caught:
                        pilot.open_image(opener, deadline, [])
                self.assertIs(caught.exception, failure)
                opener.open.assert_called_once()

    def test_late_or_stopped_open_closes_response_without_acceptance(self):
        for stopped in (False, True):
            with self.subTest(stopped=stopped):
                pilot.STOP = 0
                clock = [0.0]
                response = self.Response(b"synthetic")

                def opened(*_args, **_kwargs):
                    if stopped:
                        pilot.STOP = 2
                    else:
                        clock[0] = 100.0
                    return response

                opener = Mock()
                opener.open.side_effect = opened
                attempts = []
                with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
                    with self.assertRaisesRegex(RuntimeError, "stopped or timed out"):
                        pilot.open_image(opener, 100.0, attempts)
                opener.open.assert_called_once()
                self.assertTrue(response.closed)
                self.assertEqual(attempts[0]["outcome"], "rejected_late_or_stopped")


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name)
        self.commands = pilot.Commands(self.path, time.monotonic() + 10)
        self.addCleanup(self.commands.finish)
        self.addCleanup(setattr, pilot, "STOP", 0)

    def test_nonzero_exit_and_separate_streams(self):
        result = self.commands.run("control", [sys.executable, "-I", "-c", "import sys; print('out'); print('err', file=sys.stderr); sys.exit(7)"], 3, expected=7)
        self.assertEqual(result, (b"out\n", b"err\n", 7) if os.name != "nt" else (b"out\r\n", b"err\r\n", 7))
        self.assertEqual(self.commands.records[0]["stream_eof"], [True, True])
        self.assertIsNone(self.commands.records[0]["forced_stop"])

    def test_nonzero_exit_rejects_enabled_feature(self):
        code = "import sys; print('Feature Name : Microsoft-Windows-Subsystem-Linux'); print('State : Enabled'); sys.exit(7)"
        with self.assertRaisesRegex(RuntimeError, "unexpected exit 7"):
            self.commands.run("feature-error", [sys.executable, "-I", "-c", code], 3)
        self.assertEqual(self.commands.records[0]["exit_code"], 7)
        self.assertEqual(self.commands.records[0]["stream_eof"], [True, True])
        self.assertIsNone(self.commands.records[0]["forced_stop"])

    def test_timeout_stays_failure(self):
        with self.assertRaisesRegex(RuntimeError, "incomplete process"):
            self.commands.run("timeout", [sys.executable, "-I", "-c", "import time; time.sleep(10)"], 0.1)
        self.assertEqual(self.commands.records[0]["forced_stop"], "timeout")
        self.assertNotEqual(self.commands.records[0]["exit_code"], 0)

    def test_late_successful_exit_stays_failure(self):
        clock = [0.0]

        class Completed:
            pid, returncode = 42, 0
            stdout, stderr = io.BytesIO(), io.BytesIO()

            def poll(self):
                return 0

            def wait(self, **_kwargs):
                clock[0] = 2.0
                return 0

            def kill(self):
                raise AssertionError("An exited process must not be signaled")

        with patch.object(pilot.subprocess, "Popen", return_value=Completed()), \
                patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            with self.assertRaisesRegex(RuntimeError, "incomplete process"):
                self.commands.run("late-exit", ["synthetic"], 1)
        self.assertEqual(self.commands.records[0]["failure_reason"], "late completion")
        self.assertFalse(self.commands.records[0]["stop_requested"])

    def test_late_capture_completion_stays_failure(self):
        clock = [0.0]

        class Completed:
            pid, returncode = 42, 0
            stdout, stderr = io.BytesIO(), io.BytesIO()

            def poll(self):
                return 0

            def wait(self, **_kwargs):
                return 0

        class LateCapture:
            overflow, error, data = False, None, b""

            def __init__(self, stream):
                self.stream, self.thread, self.done = stream, self, self

            def join(self, **_kwargs):
                clock[0] = 2.0

            def is_set(self):
                return True

        with patch.object(pilot.subprocess, "Popen", return_value=Completed()), \
                patch.object(pilot, "Capture", LateCapture), \
                patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            with self.assertRaisesRegex(RuntimeError, "incomplete process"):
                self.commands.run("late-eof", ["synthetic"], 1)
        self.assertEqual(self.commands.records[0]["failure_reason"], "late completion")

    def test_overflow_is_bounded_and_fails(self):
        with patch.object(pilot, "OUTPUT_LIMIT", 32):
            with self.assertRaisesRegex(RuntimeError, "incomplete process"):
                self.commands.run("overflow", [sys.executable, "-I", "-c", "import os; os.write(1,b'x'*10000)"], 3)
        self.assertTrue(self.commands.records[0]["overflow"])
        self.assertEqual(self.commands.records[0]["stdout"]["bytes"], 32)

    def test_stop_prevents_launch_but_allows_cleanup(self):
        pilot.STOP = 15
        with self.assertRaisesRegex(RuntimeError, "stopped"):
            self.commands.run("stopped", [sys.executable, "-c", "raise RuntimeError('must not run')"], 3)
        self.assertEqual(self.commands.records, [])
        self.commands.run("cleanup", [sys.executable, "-I", "-c", "pass"], 3, cleanup=True)

    def test_absolute_deadline(self):
        with patch.dict(os.environ, {"TEST_WSL_DEADLINE": (datetime.now(timezone.utc) + timedelta(seconds=5)).isoformat()}):
            self.assertGreater(pilot.remaining_deadline("TEST_WSL_DEADLINE"), time.monotonic())
        with patch.dict(os.environ, {"TEST_WSL_DEADLINE": "2000-01-01T00:00:00+00:00"}):
            with self.assertRaisesRegex(RuntimeError, "expired"):
                pilot.remaining_deadline("TEST_WSL_DEADLINE")

    def test_partial_import_cleanup(self):
        class FakeCommands:
            def __init__(self):
                self.calls = []

            def run(self, label, argv, *_args, **_kwargs):
                self.calls.append((label, argv))
                return (b"owned\n" if label == "cleanup-inventory" else b"", b"", 0)

        fake = FakeCommands()
        state = {"import_attempted": True, "initial_distributions": [], "directory": str(self.path)}
        (self.path / "distro").mkdir()
        (self.path / "distro/partial").write_bytes(b"partial import")
        pilot.clean_distro(fake, "wsl.exe", "owned", state)
        self.assertEqual([name for name, _ in fake.calls], ["cleanup-inventory", "terminate", "unregister", "final-inventory"])
        self.assertEqual(fake.calls[2][1], ["wsl.exe", "--unregister", "owned"])
        self.assertTrue(state["distribution_absent"])
        self.assertFalse((self.path / "distro").exists())

    def test_failed_unregister_preserves_installation(self):
        class FakeCommands:
            def run(self, label, *_args, **_kwargs):
                if label == "unregister":
                    raise RuntimeError("synthetic unregister failure")
                return b"owned\n", b"", 0

        state = {"import_attempted": True, "initial_distributions": [], "directory": str(self.path)}
        (self.path / "distro").mkdir()
        with self.assertRaisesRegex(RuntimeError, "synthetic unregister failure"):
            pilot.clean_distro(FakeCommands(), "wsl.exe", "owned", state)
        self.assertTrue((self.path / "distro").exists())
        self.assertNotIn("distribution_absent", state)

    def test_preimport_failure_removes_download_without_wsl(self):
        class NoCommands:
            def run(self, *_args, **_kwargs):
                raise AssertionError("No distro command is needed before import")

        image = self.path / "ubuntu.wsl"
        image.write_bytes(b"partial or incorrect download")
        state = {"import_attempted": False, "directory": str(self.path)}
        pilot.clean_distro(NoCommands(), "wsl.exe", "owned", state)
        self.assertFalse(image.exists())

    def test_final_write_uses_effective_cleanup_deadline(self):
        clock = [0.0]
        state = {"name": "owned", "directory": str(self.path), "import_attempted": False}
        pilot.write_json(self.path / "state.json", state)
        write = pilot.write_json

        def late_write(path, data):
            write(path, data)
            if path.name == "cleanup.json":
                clock[0] = 200.0

        with patch.object(pilot.sys, "platform", "win32"), \
                patch.object(pilot.platform, "machine", return_value="AMD64"), \
                patch.object(pilot, "owned_paths", return_value=("owned", self.path)), \
                patch.object(pilot, "remaining_deadline", return_value=1000.0), \
                patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]), \
                patch.object(pilot, "write_json", side_effect=late_write), \
                patch.dict(os.environ, {"SYSTEMROOT": "/synthetic-windows"}), \
                patch.object(pilot.subprocess, "Popen", side_effect=AssertionError("No process may start")):
            self.assertEqual(pilot.pilot(None, cleanup_only=True), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
