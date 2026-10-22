#!/usr/bin/env python3
"""Offline controls for the WSL pager pilot. Never imports or starts a distro."""

from dataclasses import replace
from datetime import datetime, timedelta, timezone
import hashlib
import importlib.util
import io
import json
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
sys.modules[spec.name] = pilot
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


def resolved_legacy_plan(command_contracts=False, include_process=False):
    return pilot.resolve_profile(
        Path("custom.test"),
        process_binary=Path("process.test") if command_contracts or include_process else None,
        command_contracts=command_contracts,
        app_binary=Path("app.test") if command_contracts else None,
        images_binary=Path("images.test") if command_contracts else None)


def only_phase(plan, label):
    return replace(plan, phases=(next(phase for phase in plan.phases if phase.label == label),))


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
        plan = only_phase(resolved_legacy_plan(include_process=True), "process")
        commands = Mock(deadline=200.0)
        commands.run.return_value = process_passing_output(), b"", 0
        hashes = {"process.test": "a" * 64}
        with patch.object(pilot.time, "monotonic", return_value=30.001):
            with self.assertRaisesRegex(RuntimeError, "Insufficient work time"):
                pilot.run_profile(commands, ["user"], plan, hashes, {})
        commands.run.assert_not_called()
        with patch.object(pilot.time, "monotonic", return_value=30.0), \
                patch.object(pilot, "digest", return_value="a" * 64):
            pilot.run_profile(commands, ["user"], plan, hashes, {})
        commands.run.assert_called_once_with("process", [
            "user", "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s",
            "/home/cli-compat/payload/process.test", "-test.run=" + pilot.PROCESS_SELECTOR,
            "-test.timeout=150s", "-test.v"], 170)

    def test_process_stage_does_not_accept_a_false_command_result(self):
        plan = only_phase(resolved_legacy_plan(include_process=True), "process")
        with patch.object(pilot.time, "monotonic", return_value=0):
            for response in ((process_passing_output(), b"", 1),
                             (process_passing_output(), b"unexpected", 0),
                             (b"PASS\n", b"", 0)):
                commands, result = Mock(deadline=200.0), {}
                commands.run.return_value = response
                with self.subTest(code=response[2], stderr=response[1]):
                    with self.assertRaises(RuntimeError):
                        pilot.run_profile(commands, ["user"], plan, {}, result)
                commands.run.assert_called_once()
                self.assertNotIn("process_tests", result)

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


class CommandContractTests(unittest.TestCase):
    def setUp(self):
        self.raw = pilot.COMMAND_CASES_PATH.read_bytes()
        self.suites = json.loads(self.raw)
        processes = patch.object(pilot.subprocess, "Popen", side_effect=AssertionError("No process may start"))
        network = patch.object(pilot.urllib.request, "build_opener", side_effect=AssertionError("No network may start"))
        processes.start()
        network.start()
        self.addCleanup(processes.stop)
        self.addCleanup(network.stop)

    @staticmethod
    def names(cases):
        return set(cases) | {root + "/" + child for root, children in cases.items() for child in children}

    @classmethod
    def output(cls, cases):
        return ("".join(f"=== RUN   {name}\n--- PASS: {name} (0.01s)\n"
                        for name in sorted(cls.names(cases))) + "PASS\n").encode()

    def load_fixture(self, raw):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "cases.json"
            path.write_bytes(raw)
            with patch.object(pilot, "COMMAND_CASES_PATH", path):
                return pilot.load_command_cases()

    def test_fixed_inventory_and_literal_suffixes(self):
        suites, checksum = pilot.load_command_cases()
        self.assertEqual(suites, self.suites)
        self.assertEqual(checksum, hashlib.sha256(self.raw).hexdigest())
        self.assertEqual(set(suites), {"custom", "images", "process", "app"})
        self.assertEqual((len(suites["custom"]), len(self.names(suites["custom"]))), (4, 9))
        self.assertEqual((len(suites["images"]), len(self.names(suites["images"]))), (6, 62))
        self.assertEqual((len(suites["process"]), len(self.names(suites["process"]))), (65, 577))
        self.assertEqual((len(suites["app"]), len(self.names(suites["app"]))), (2, 12))
        self.assertEqual(set(suites["app"]), {"TestRequestConfigurationRestoresEnvironment",
                                             "TestRequestConfigurationLeavesOtherEnvironmentCasesUnchanged"})
        self.assertEqual(suites["process"]["TestMainBashRequestConfiguration"], [
            "placement_precedence_and_error", "root_and_body_project", "equals_empty",
            "separate_empty", "completion_stays_local",
        ])
        self.assertIn("absent/", suites["images"]["TestImageInlinePreferencePrecedence"])
        self.assertIn("absent/#01", suites["images"]["TestImageInlinePreferencePrecedence"])
        self.assertIn("#00", suites["process"]["TestMainGlobalFlagsDeepAdminAndCustomCommands"])
        self.assertIn("missing_required_flag/", suites["process"]["TestMainLocalErrorsStayOnStderr"])
        self.assertIn('{"error":true}/--format/text/--format-error/json',
                      suites["process"]["TestMainAPIErrorsFormatPrecedenceAndExtraction"])
        self.assertIn('"synthetic_private_response_detail"/--format-error/json',
                      suites["process"]["TestMainAPIErrorsWithUnexpectedJSONShapes"])
        selected = set().union(*(set(cases) for cases in suites.values()))
        self.assertEqual(len(selected), 77)
        self.assertFalse(set(suites["custom"]) & set(suites["process"]))
        self.assertFalse(selected & (set(pilot.PROCESS_CASES) | {pilot.ROOT}))
        self.assertFalse(selected & {"TestMainDispatchProcess", "TestMainWrappedExitCodeProcess",
                                    "TestRequestConfigurationChildEnvironment"})

    def test_inventory_rejects_malformed_duplicate_and_nonregular_inputs(self):
        duplicate_root = self.raw.replace(b'"custom": {',
                                         b'"custom": {"TestRootRequestFlagsPreserveOwnershipAndMetadata": [],', 1)
        for raw in (b"", b"{", b"\xff", b"[]", b"null", b'{"custom": {},' + self.raw[1:], duplicate_root):
            with self.subTest(raw=raw[:70]):
                with self.assertRaises((RuntimeError, ValueError, UnicodeError)):
                    self.load_fixture(raw)
        for symlink, regular in ((True, True), (False, False)):
            path = Mock()
            path.is_symlink.return_value = symlink
            path.is_file.return_value = regular
            with self.subTest(symlink=symlink, regular=regular):
                with patch.object(pilot, "COMMAND_CASES_PATH", path):
                    with self.assertRaisesRegex(RuntimeError, "regular.*inventory"):
                        pilot.load_command_cases()
                path.read_bytes.assert_not_called()

    def test_inventory_rejects_invalid_shapes_and_counts(self):
        for label in ("missing suite", "extra suite", "swapped suites", "wrong root count",
                      "wrong named count", "nonmapping suite", "nonlist children",
                      "duplicate child", "empty child", "whitespace child", "nonstring child", "invalid root"):
            with self.subTest(case=label):
                suites = json.loads(self.raw)
                root = "TestMainGlobalFlagsPrecedence"
                if label == "missing suite":
                    del suites["custom"]
                elif label == "extra suite":
                    suites["other"] = {}
                elif label == "swapped suites":
                    suites["custom"], suites["process"] = suites["process"], suites["custom"]
                elif label == "wrong root count":
                    del suites["process"][root]
                elif label == "wrong named count":
                    suites["process"][root].pop()
                elif label == "nonmapping suite":
                    suites["custom"] = []
                elif label == "nonlist children":
                    suites["process"][root] = "environment"
                elif label == "duplicate child":
                    suites["process"][root][1] = suites["process"][root][0]
                elif label == "empty child":
                    suites["process"][root][0] = ""
                elif label == "whitespace child":
                    suites["process"][root][0] = "unexpected child"
                elif label == "nonstring child":
                    suites["process"][root][0] = 7
                else:
                    suites["process"]["TestUnexpected/root"] = suites["process"].pop(root)
                with self.assertRaisesRegex(RuntimeError, "inventory|case"):
                    self.load_fixture(json.dumps(suites).encode())

    def test_inventory_rejects_helper_baseline_and_cross_package_roots(self):
        forbidden = ["TestMainDispatchProcess", "TestMainWrappedExitCodeProcess",
                     "TestRequestConfigurationChildEnvironment", pilot.ROOT,
                     *pilot.PROCESS_CASES, "TestRootRequestFlagsPreserveOwnershipAndMetadata"]
        for root in forbidden:
            with self.subTest(root=root):
                suites = json.loads(self.raw)
                # Replacing a zero-child root keeps both count checks satisfied.
                del suites["process"]["TestMainPreservesWrappedExitCode"]
                suites["process"][root] = []
                with self.assertRaisesRegex(RuntimeError, "case"):
                    self.load_fixture(json.dumps(suites).encode())

    def test_supplement_suites_require_exact_output(self):
        plan = resolved_legacy_plan(command_contracts=True)
        for package in ("custom", "process", "images", "app"):
            selected = only_phase(plan, package + "-contracts")
            cases = self.suites[package]
            good = self.output(cases)
            name = next(name for name in sorted(self.names(cases)) if "/" in name)
            run = f"=== RUN   {name}\n".encode()
            passed = f"--- PASS: {name} (0.01s)\n".encode()
            bad = {
                "missing run": good.replace(run, b""),
                "missing pass": good.replace(passed, b""),
                "duplicate run": good + run,
                "duplicate pass": good + passed,
                "extra test": good + b"=== RUN   TestOther\n--- PASS: TestOther (0.01s)\n",
                "skip": good.replace(passed, f"--- SKIP: {name} (0.01s)\n".encode()),
                "fail": good.replace(passed, f"--- FAIL: {name} (0.01s)\n".encode()),
                "wrong package": self.output(self.suites["process" if package == "custom" else "custom"]),
                "missing conclusion": good.removesuffix(b"PASS\n"),
                "duplicate conclusion": good + b"PASS\n",
                "empty selection": b"PASS\n",
                "invalid encoding": b"\xff" + good,
                "panic": good + b"panic: synthetic\n",
            }
            responses = [(label, (raw, b"", 0)) for label, raw in bad.items()]
            responses.extend([("nonzero exit", (good, b"", 7)), ("stderr", (good, b"unexpected\n", 0))])
            for label, response in responses:
                with self.subTest(package=package, case=label):
                    commands, result = Mock(deadline=1000.0), {}
                    commands.run.return_value = response
                    with patch.object(pilot.time, "monotonic", return_value=0.0):
                        with self.assertRaises((RuntimeError, UnicodeError)):
                            pilot.run_profile(commands, ["user"], selected, {}, result)
                    commands.run.assert_called_once()
                    self.assertNotIn(selected.phases[0].result_path[0], result)

    def test_custom_admission_and_both_fixed_commands(self):
        for supplement in (False, True):
            label = "custom-contracts" if supplement else "pager"
            plan = only_phase(resolved_legacy_plan(command_contracts=supplement), label)
            cases = self.suites["custom"] if supplement else None
            commands = Mock(deadline=200.0)
            commands.run.return_value = (self.output(cases) if supplement else passing_output()), b"", 0
            with self.subTest(supplement=supplement):
                with patch.object(pilot.time, "monotonic", return_value=130.001):
                    with self.assertRaisesRegex(RuntimeError, "Insufficient work time"):
                        pilot.run_profile(commands, ["user"], plan, {}, {})
                commands.run.assert_not_called()
                result = {}
                with patch.object(pilot.time, "monotonic", return_value=130.0):
                    pilot.run_profile(commands, ["user"], plan, {}, result)
                selector = "^(" + "|".join(cases) + ")$" if supplement else "^" + pilot.ROOT + "$"
                commands.run.assert_called_once_with(label, [
                    "user", "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "60s",
                    "/home/cli-compat/payload/custom.test", "-test.run=" + selector, "-test.timeout=45s", "-test.v"], 70)
                self.assertEqual(set(result["tests"]), self.names(cases) if supplement else pilot.TESTS)

    def test_process_supplement_preserves_full_admission_and_command(self):
        cases = self.suites["process"]
        plan = only_phase(resolved_legacy_plan(command_contracts=True), "process-contracts")
        commands = Mock(deadline=200.0)
        commands.run.return_value = self.output(cases), b"", 0
        hashes = {"process.test": "a" * 64}
        with patch.object(pilot.time, "monotonic", return_value=30.001):
            with self.assertRaisesRegex(RuntimeError, "Insufficient work time"):
                pilot.run_profile(commands, ["user"], plan, hashes, {})
        commands.run.assert_not_called()
        result = {}
        with patch.object(pilot.time, "monotonic", return_value=30.0), \
                patch.object(pilot, "digest", return_value="a" * 64):
            pilot.run_profile(commands, ["user"], plan, hashes, result)
        self.assertEqual(set(result["process_tests"]), self.names(cases))
        commands.run.assert_called_once_with("process-contracts", [
            "user", "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s",
            "/home/cli-compat/payload/process.test", "-test.run=^(" + "|".join(cases) + ")$",
            "-test.timeout=150s", "-test.v"], 170)

    def test_cli_rejects_missing_payloads_and_unknown_flags(self):
        for argv in (["--command-contracts"], ["--command-contracts", "--binary", "custom.test"],
                     ["--command-contracts", "--process-binary", "process.test"],
                     ["--command-contracts", "--binary", "custom.test", "--process-binary", "process.test"],
                     ["--command-contracts", "--binary", "custom.test", "--process-binary", "process.test",
                      "--app-binary", "app.test"], ["--unknown"]):
            with self.subTest(argv=argv):
                with patch.object(pilot.sys, "argv", ["check-wsl-pager.py", *argv]), \
                        patch.object(pilot.signal, "signal"), \
                        patch.object(pilot.sys, "stderr", io.StringIO()), patch.object(pilot, "pilot") as run:
                    with self.assertRaises(SystemExit) as caught:
                        pilot.main()
                self.assertEqual(caught.exception.code, 2)
                run.assert_not_called()

    def test_cli_preserves_profile_and_cleanup_flags(self):
        for argv, selection, cleanup, payloads in (
            (["--command-contracts", "--binary", "custom.test", "--process-binary", "process.test",
              "--app-binary", "app.test", "--images-binary", "images.test"],
             "command-contracts", False, {"custom.test", "process.test", "app.test", "images.test"}),
            (["--cleanup", "--command-contracts"], "command-contracts", True, set()),
            (["--binary", "custom.test"], "pager-and-process", False, {"custom.test"}),
        ):
            with self.subTest(argv=argv), \
                    patch.object(pilot.sys, "argv", ["check-wsl-pager.py", *argv]), \
                    patch.object(pilot.signal, "signal"), \
                    patch.object(pilot, "resolve_profile", wraps=pilot.resolve_profile) as resolve, \
                    patch.object(pilot, "pilot", return_value=0) as run:
                self.assertEqual(pilot.main(), 0)
            resolve.assert_called_once()
            run.assert_called_once()
            self.assertEqual(run.call_args.kwargs, {})
            self.assertEqual(len(run.call_args.args), 1)
            plan = run.call_args.args[0]
            self.assertEqual(plan.selection, selection)
            self.assertIs(plan.cleanup_only, cleanup)
            self.assertEqual({payload.filename for payload in plan.payloads}, payloads)

    def test_resolver_requires_all_payloads_before_preparation(self):
        for binary, process_binary in ((None, None), (Path("custom.test"), None), (None, Path("process.test")),
                                       (Path("custom.test"), Path("process.test"))):
            with self.subTest(binary=binary, process=process_binary):
                with patch.object(pilot, "load_command_cases") as load, \
                        patch.object(pilot, "require_native_windows_process") as native:
                    with self.assertRaisesRegex(RuntimeError, "all four test binaries"):
                        pilot.resolve_profile(binary, process_binary=process_binary, command_contracts=True)
                load.assert_not_called()
                native.assert_not_called()

    def test_pilot_routes_only_the_fixed_supplement(self):
        outputs = {package + "-contracts": self.output(cases) for package, cases in self.suites.items()}
        class FakeCommands:
            deadline = 1000.0

            def __init__(self):
                self.records, self.calls = [], []

            def run(self, label, *args, **_kwargs):
                self.calls.append((label, *args))
                if label in outputs:
                    return outputs[label], b"", 0
                replies = {
                    "source": (b"source\ntree\n", b"", 0),
                    "source-final": (b"source\ntree\n", b"", 0),
                    "wsl-version": (b"synthetic version\n", b"", 0),
                    "initial-inventory": (b"", b"", 0),
                    "feature": (b"Feature Name : Microsoft-Windows-Subsystem-Linux\nState : Enabled\n", b"", 0),
                    "import": (b"", b"", 0),
                    "version-one": (b"owned Stopped 1\n", b"", 0),
                    "user": (b"", b"", 0),
                    "ubuntu-identity": (b"ID=ubuntu\n", b"", 0),
                    "exit-control": (b"wsl-control-out\n", b"wsl-control-err\n", 7),
                }
                return replies[label]

            def finish(self):
                pass

        with tempfile.TemporaryDirectory() as directory:
            parent = Path(directory)
            custom, process, app, images = (parent / name for name in ("custom.test", "process.test", "app.test", "images.test"))
            for path in (custom, process, app, images):
                path.write_bytes(b"synthetic")
            owned = parent / "owned"
            commands = FakeCommands()
            custom_names = self.names(self.suites["custom"])
            process_names = self.names(self.suites["process"])
            app_names = self.names(self.suites["app"])
            images_names = self.names(self.suites["images"])
            with patch.object(pilot, "load_command_cases", return_value=(self.suites, "a" * 64)) as load, \
                    patch.object(pilot, "require_native_windows_process", return_value={}), \
                    patch.object(pilot, "owned_paths", return_value=("owned", owned)), \
                    patch.object(pilot, "remaining_deadline", return_value=1000.0), \
                    patch.object(pilot.time, "monotonic", return_value=0.0), \
                    patch.object(pilot, "Commands", return_value=commands), \
                    patch.object(pilot, "require_linux_binary", return_value="b" * 64), \
                    patch.object(pilot, "digest", side_effect=lambda path: "a" * 64 if path == pilot.COMMAND_CASES_PATH else "b" * 64), \
                    patch.object(pilot.shutil, "disk_usage", return_value=Mock(free=6 * 1024**3)), \
                    patch.object(pilot, "download_image", return_value=0), \
                    patch.object(pilot, "copy_payload") as copy, patch.object(pilot, "clean_distro"), \
                    patch.dict(os.environ, {"SYSTEMROOT": str(parent / "windows")}):
                plan = pilot.resolve_profile(custom, process_binary=process, command_contracts=True,
                                             app_binary=app, images_binary=images)
                self.assertEqual(pilot.pilot(plan), 0)
            load.assert_called_once_with()
            phases = [call for call in commands.calls if call[0] in outputs]
            self.assertEqual([call[0] for call in phases],
                             ["custom-contracts", "process-contracts", "images-contracts", "app-contracts"])
            self.assertEqual([call[2] for call in phases], [70, 170, 70, 70])
            for call, payload in zip(phases, ("custom", "process", "images", "app")):
                self.assertIn("/home/cli-compat/payload/" + payload + ".test", call[1])
            self.assertEqual(copy.call_args_list[0].args[3:], (custom.resolve(), "/home/cli-compat/payload/custom.test", "b" * 64, ""))
            self.assertEqual(copy.call_args_list[1].args[3:], (process.resolve(), "/home/cli-compat/payload/process.test", "b" * 64, "process-"))
            self.assertEqual(copy.call_args_list[2].args[3:], (app.resolve(), "/home/cli-compat/payload/app.test", "b" * 64, "app-"))
            self.assertEqual(copy.call_args_list[3].args[3:], (images.resolve(), "/home/cli-compat/payload/images.test", "b" * 64, "images-"))
            result = json.loads((owned / "result.json").read_text())
            self.assertEqual(result["images_tests"], sorted(images_names))
            self.assertEqual(result["images_binary_sha256"], "b" * 64)
            self.assertEqual(result["selection"], "command-contracts")
            self.assertEqual(result["command_cases_sha256"], "a" * 64)
            self.assertEqual(result["tests"], sorted(custom_names))
            self.assertEqual(result["process_tests"], sorted(process_names))
            self.assertEqual(result["app_tests"], sorted(app_names))
            self.assertEqual(result["app_binary_sha256"], "b" * 64)

    def test_cleanup_does_not_require_inventory_or_payloads(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            state = {"name": "owned", "directory": str(path), "import_attempted": False}
            (path / "state.json").write_text(json.dumps(state))
            commands = Mock(deadline=1000.0, records=[])
            with patch.object(pilot, "require_native_windows_process", return_value={}), \
                    patch.object(pilot, "owned_paths", return_value=("owned", path)), \
                    patch.object(pilot, "remaining_deadline", return_value=1000.0), \
                    patch.object(pilot.time, "monotonic", return_value=0.0), \
                    patch.object(pilot, "Commands", return_value=commands), \
                    patch.object(pilot, "load_command_cases", side_effect=FileNotFoundError("missing inventory")) as load, \
                    patch.object(pilot, "load_shell_receipts", side_effect=AssertionError("receipt helper loaded")), \
                    patch.object(pilot, "clean_distro") as clean, \
                    patch.dict(os.environ, {"SYSTEMROOT": str(path / "windows")}):
                self.assertEqual(pilot.pilot(pilot.resolve_profile(cleanup_only=True, command_contracts=True)), 0)
            load.assert_not_called()
            commands.run.assert_not_called()
            commands.finish.assert_called_once()
            self.assertEqual(clean.call_args.args[2:], ("owned", state))


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
            self.assertEqual(pilot.pilot(pilot.resolve_profile(cleanup_only=True)), 1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
