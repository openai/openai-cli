#!/usr/bin/env python3
"""Offline controls for the WSL pager pilot. Never imports or starts a distro."""

from datetime import datetime, timedelta, timezone
import importlib.util
import io
import json
import os
from pathlib import Path
import sys
import tempfile
import time
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("wsl_pager", Path(__file__).with_name("check-wsl-pager.py"))
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)


def passing_output():
    root = pilot.ROOT
    return (f"=== RUN   {root}\n=== RUN   {root}/LookPath\n"
            f"=== RUN   {root}/ForkExec\n--- PASS: {root} (0.02s)\n"
            f"    --- PASS: {root}/LookPath (0.01s)\n"
            f"    --- PASS: {root}/ForkExec (0.01s)\nPASS\n").encode()


class MetadataTests(unittest.TestCase):
    def test_enabled_feature(self):
        row = {"Name": "Microsoft-Windows-Subsystem-Linux", "InstallState": 1}
        for encoding in ("utf-8", "utf-8-sig"):
            with self.subTest(encoding=encoding):
                pilot.require_feature_enabled((json.dumps([row]) + "\r\n").encode(encoding), pilot.FEATURE_PROGRESS)

    def test_unverified_feature(self):
        good = {"Name": "Microsoft-Windows-Subsystem-Linux", "InstallState": 1}
        bad = {"missing": [], "duplicate": [good, good], "object": good, "null": None,
               "wrong name": [dict(good, Name="VirtualMachinePlatform")], "missing state": [{"Name": good["Name"]}],
               "extra field": [dict(good, Other=1)], "non-object row": [1]}
        for label, value in (("disabled", 2), ("absent", 3), ("unknown", 4), ("zero", 0),
                             ("boolean", True), ("string", "1"), ("float", 1.0), ("null state", None)):
            bad[label] = [dict(good, InstallState=value)]
        for label, rows in bad.items():
            with self.subTest(label=label):
                with self.assertRaisesRegex(RuntimeError, "not verified as already enabled"):
                    pilot.require_feature_enabled(json.dumps(rows).encode(), pilot.FEATURE_PROGRESS)

    def test_malformed_feature(self):
        for raw in (b"Enabled", b"\xff", b"[{\"Name\":\"other\",\"Name\":\"Microsoft-Windows-Subsystem-Linux\",\"InstallState\":1}]"):
            with self.subTest(raw=raw):
                with self.assertRaisesRegex(RuntimeError, "Malformed|Duplicate"):
                    pilot.require_feature_enabled(raw, pilot.FEATURE_PROGRESS)

    def test_powershell_startup_control(self):
        stdout, stderr = b"powershell-control-out", b"powershell-control-err"
        pilot.require_powershell_control(stdout, stderr)
        for out, err in ((b"", b""), (stderr, stdout), (stdout + b"\n", stderr), (stdout, stderr + b"unexpected")):
            with self.subTest(stdout=out, stderr=err):
                with self.assertRaisesRegex(RuntimeError, "startup control"):
                    pilot.require_powershell_control(out, err)

    def test_feature_query_progress(self):
        data = b'[{"Name":"Microsoft-Windows-Subsystem-Linux","InstallState":1}]'
        for stderr in (b"", b"feature-query-start\r\n", pilot.FEATURE_PROGRESS + b"unexpected",
                       b"feature-query-complete\r\nfeature-query-start\r\n"):
            with self.subTest(stderr=stderr):
                with self.assertRaisesRegex(RuntimeError, "feature progress"):
                    pilot.require_feature_enabled(data, stderr)

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
