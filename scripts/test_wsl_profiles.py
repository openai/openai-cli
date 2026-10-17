#!/usr/bin/env python3
"""Offline controls for fixed WSL profiles. All process and distribution operations are mocked."""

from copy import deepcopy
import base64
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import re
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch


spec = importlib.util.spec_from_file_location("wsl_profiles", Path(__file__).with_name("check-wsl-pager.py"))
pilot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pilot)

WELCOME = "TestMainWelcomeBareInvocationStaysOffline"
DYNAMIC = WELCOME + "/<loopback>"
WELCOME_NAMES = {WELCOME, DYNAMIC, WELCOME + "/not%url"}
NATIVE_NAMES = {
    "TestMainNativeShell", "TestMainNativeShell/bash",
    "TestMainNativeShell/bash/installed_help_uses_command_name",
    "TestMainNativeShell/bash/copy_local_help_with_implicit_lookup_enabled",
    "TestMainNativeShell/bash/copy_typo_suggestion_with_another_openai_on_PATH",
}
COMPLETION_NAMES = {
    "single quoted assigned file", "double quoted assigned file", "double quoted apostrophe",
    "escaped assigned file", "separate quoted file", "unquoted assigned control",
    "UTF-8 midline file with later flag",
}


def test_output(names):
    return ("".join(f"=== RUN   {name}\n--- PASS: {name} (0.01s)\n" for name in sorted(names))
            + "PASS\n").encode()


def welcome_output(url="http://127.0.0.1:12345"):
    return test_output((WELCOME_NAMES - {DYNAMIC}) | {WELCOME + "/" + url})


def captured_fields(stream, raw):
    return {stream + "_base64": base64.b64encode(raw).decode("ascii"),
            stream + "_bytes": len(raw), stream + "_sha256": hashlib.sha256(raw).hexdigest()}


def valid_receipt(case):
    receipt = {"case": case, "exit_code": 0, "cleanup_complete": True, "failures": []}
    if case == "models-filter-sort-limit":
        output = ('{"id":"demo-text-2026-10-01-001","object":"model","created":1700000000,'
                  '"owned_by":"system","shutdown_date":"2030-01-01"}\n')
        receipt.update(cli_call_count=1, cli_exit_code=0, request_count=1, accepted_request_count=1,
                       stdout_bytes=121, stderr_bytes=0, response_bytes=5834,
                       stdout_utf8=output, stdout_sha256=hashlib.sha256(output.encode()).hexdigest(),
                       response_sha256="65a35e98b76235a982e331fe094c5d044e3e6380c7bf2de51655459ff77f6d47",
                       stdout_eof=True, stderr_eof=True, openai_settings_absent=True, fixture_thread_joined=True)
    else:
        receipt.update(setup_call_count=6, fresh_shell_count=3, expected_setup_calls=6,
                       expected_fresh_shells=3, setup_stdout_eof=True, setup_stderr_eof=True, pty_eof=True,
                       setup_calls=[{"action": action, "exit_code": 0, "stdout_eof": True, "stderr_eof": True}
                                    for action in ("install", "install-again", "remove", "remove-again", "reenable", "remove-final")],
                       fresh_shells=[{"checkpoint": checkpoint, "exit_code": 0, "pty_eof": True, "forced_cleanup": False}
                                     for checkpoint in ("fresh-installed", "fresh-removed", "fresh-reenabled")])
        for item in receipt["setup_calls"]:
            message = (b"Tab shortcut setup removed. Open a new terminal to finish.\n"
                       if item["action"].startswith("remove") else
                       b"Tab setup saved for future terminals. Existing custom bindings are preserved.\n")
            item.update(captured_fields("stdout", b""))
            item.update(captured_fields("stderr", message))
        for item in receipt["fresh_shells"]:
            item.update(captured_fields("pty", b"__PROFILE_READY__> synthetic checkpoint\r\n"))
        source = b"# Synthetic system profile\n"
        sources = [{"path": "/etc/profile", "present": True, "bytes": len(source),
                    "sha256": hashlib.sha256(source).hexdigest(),
                    "content_base64": base64.b64encode(source).decode("ascii"),
                    "identity": {"device": 1, "inode": 2, "mode": 0o100644, "uid": 0, "gid": 0,
                                 "size": len(source), "mtime_ns": 1, "ctime_ns": 1}},
                   {"path": "/etc/bash.bashrc", "present": False}]
        receipt.update(system_sources=sources, system_sources_after=deepcopy(sources), system_sources_unchanged=True)
    return receipt


def receipt_output(case, receipt=None):
    if receipt is None:
        receipt = valid_receipt(case)
    encoded = receipt if isinstance(receipt, str) else json.dumps(receipt)
    return f"PASS: {case}\nRECEIPT: {encoded}\n".encode()


def completion_output():
    return ("GNU bash, version synthetic\n"
            + "".join(f"PASS: {name}\n" for name in sorted(COMPLETION_NAMES))).encode()


class LocalInventoryTests(unittest.TestCase):
    def setUp(self):
        self.raw = Path(__file__).with_name("wsl-local-state-cases.json").read_bytes()
        self.suites = json.loads(self.raw)

    def load(self, suites=None, raw=None):
        data = raw if raw is not None else json.dumps(suites).encode()
        path = Mock()
        path.is_symlink.return_value = False
        path.is_file.return_value = True
        path.read_bytes.return_value = data
        with patch.object(pilot, "LOCAL_CASES_PATH", path):
            return pilot.load_local_cases()

    def test_fixed_five_payload_inventory(self):
        suites, digest = self.load(raw=self.raw)
        self.assertEqual(set(suites), {"imageprefs", "custom", "autocomplete", "process", "clihelp"})
        expected = {"imageprefs": (9, 21, 30), "custom": (10, 5, 15),
                    "autocomplete": (3, 29, 32), "process": (28, 114, 142), "clihelp": (3, 1, 4)}
        total = [0, 0, 0]
        for package, counts in expected.items():
            cases = suites[package]
            observed = (len(cases), sum(map(len, cases.values())), len(pilot.test_names(cases)))
            self.assertEqual(observed, counts)
            total = [left + right for left, right in zip(total, observed)]
        self.assertEqual(total, [53, 170, 223])
        self.assertEqual(digest, hashlib.sha256(self.raw).hexdigest())
        self.assertEqual(suites["process"][WELCOME], ["<loopback>", "not%url"])
        self.assertEqual(len(suites["process"]["TestMainCommandSubgroupsBareInvocation"]), 10)
        for root in ("TestMainImageModelsRequestDeadlineCancelsHTTP",
                     "TestMainImageModelsInterruptPreservesCompletedResults",
                     "TestMainDispatchOutputManpagesDeepSubgroups"):
            self.assertEqual(suites["process"][root], [])
        self.assertEqual(suites["clihelp"], {
            "TestHelpInvocationUsesSameExecutableOnPath": [],
            "TestHelpInvocationFollowsInstalledSymlink": [],
            "TestHelpInvocationKeepsPowerShellScriptOnPath": ["dangling_symlink"],
        })
        self.assertEqual(set(suites["autocomplete"]), {
            "TestPickerRenameNoReplace", "TestPickerInstallRoundTrip", "TestPickerRemoveRestoresAbsentProfile",
        })
        self.assertEqual(suites["autocomplete"]["TestPickerInstallRoundTrip"], [
            shell + "/" + original for shell in ("bash", "zsh", "fish")
            for original in ("", "#_personal_setting", "#_personal_setting_", "\\ufeff#_Windows_setting__")
        ])

    def test_all_payload_names_require_execution(self):
        for package, cases in self.suites.items():
            names = pilot.test_names(cases)
            raw = test_output(names)
            if package == "process":
                raw = raw.replace(DYNAMIC.encode(), (WELCOME + "/http://127.0.0.1:12345").encode())
            with self.subTest(package=package):
                pilot.require_test_pass(raw, b"", 0, names, package, package == "process")
                omitted = sorted(names - {DYNAMIC})[0]
                incomplete = raw.replace(f"=== RUN   {omitted}\n".encode(), b"")
                with self.assertRaises(RuntimeError):
                    pilot.require_test_pass(incomplete, b"", 0, names, package, package == "process")

    def test_duplicate_json_keys_reject(self):
        root = next(iter(self.suites["imageprefs"]))
        for raw in (
            self.raw.replace(b'"imageprefs": {', b'"imageprefs": {}, "imageprefs": {', 1),
            self.raw.replace(json.dumps(root).encode() + b":", (json.dumps(root) + ": [], "
                             + json.dumps(root) + ":").encode(), 1),
        ):
            with self.subTest(raw=raw[:80]):
                with self.assertRaisesRegex(RuntimeError, "Duplicated"):
                    self.load(raw=raw)

    def test_package_root_and_descendant_counts_reject(self):
        variants = []
        missing_package = deepcopy(self.suites)
        del missing_package["clihelp"]
        variants.append(missing_package)
        extra_package = deepcopy(self.suites)
        extra_package["unreviewed"] = {}
        variants.append(extra_package)
        missing_root = deepcopy(self.suites)
        missing_root["custom"].pop(next(iter(missing_root["custom"])))
        variants.append(missing_root)
        missing_child = deepcopy(self.suites)
        missing_child["clihelp"]["TestHelpInvocationKeepsPowerShellScriptOnPath"] = []
        variants.append(missing_child)
        extra_child = deepcopy(self.suites)
        extra_child["clihelp"]["TestHelpInvocationFollowsInstalledSymlink"] = ["unreviewed"]
        variants.append(extra_child)
        for suites in variants:
            with self.subTest(keys=list(suites)):
                with self.assertRaises(RuntimeError):
                    self.load(suites)

    def test_duplicate_children_reject(self):
        suites = deepcopy(self.suites)
        suites["process"][WELCOME] = ["<loopback>", "<loopback>"]
        with self.assertRaisesRegex(RuntimeError, "duplicated"):
            self.load(suites)

    def test_overlap_with_existing_profiles_and_helpers_rejects(self):
        previous, _ = pilot.load_command_cases()
        excluded = [next(iter(pilot.PROCESS_CASES)), next(iter(previous["custom"])),
                    next(iter(previous["process"])), "TestMainDispatchProcess",
                    "TestImagePreferenceWaitingWriterProcess"]
        excluded.extend(previous["app"])
        for root in excluded:
            suites = deepcopy(self.suites)
            cases = suites["imageprefs"]
            cases[root] = cases.pop("TestImagePreferenceDefaultAndReplacement")
            with self.subTest(root=root):
                with self.assertRaises(RuntimeError):
                    self.load(suites)

    def test_cross_payload_root_overlap_rejects(self):
        suites = deepcopy(self.suites)
        cases = suites["clihelp"]
        cases["TestImagePreferenceDefaultAndReplacement"] = cases.pop("TestHelpInvocationUsesSameExecutableOnPath")
        with self.assertRaises(RuntimeError):
            self.load(suites)

    def test_invalid_root_and_child_shapes_reject(self):
        for children in (None, "child", [""], ["two words"], [False], [1]):
            suites = deepcopy(self.suites)
            suites["clihelp"]["TestHelpInvocationKeepsPowerShellScriptOnPath"] = children
            with self.subTest(children=children):
                with self.assertRaises(RuntimeError):
                    self.load(suites)
        suites = deepcopy(self.suites)
        suites["clihelp"]["TestUnexpected/child"] = suites["clihelp"].pop("TestHelpInvocationUsesSameExecutableOnPath")
        with self.assertRaises(RuntimeError):
            self.load(suites)

    def test_dynamic_slot_requires_the_exact_root_and_child(self):
        for child in ("literal", "prefix/<loopback>", "<loopback>/suffix"):
            suites = deepcopy(self.suites)
            suites["process"][WELCOME][0] = child
            with self.subTest(child=child):
                with self.assertRaises(RuntimeError):
                    self.load(suites)
        suites = deepcopy(self.suites)
        suites["clihelp"]["TestHelpInvocationKeepsPowerShellScriptOnPath"] = ["<loopback>"]
        with self.assertRaises(RuntimeError):
            self.load(suites)

    def test_missing_or_linked_inventory_rejects_before_read(self):
        for linked, regular in ((True, True), (False, False)):
            path = Mock()
            path.is_symlink.return_value = linked
            path.is_file.return_value = regular
            with self.subTest(linked=linked, regular=regular), patch.object(pilot, "LOCAL_CASES_PATH", path):
                with self.assertRaises(RuntimeError):
                    pilot.load_local_cases()
                path.read_bytes.assert_not_called()


class DynamicWelcomeTests(unittest.TestCase):
    def accept(self, raw, stderr=b"", code=0):
        return pilot.require_test_pass(raw, stderr, code, WELCOME_NAMES, "welcome", True)

    def test_exact_ipv4_and_ipv6_ports_preserve_raw_identity(self):
        for host in ("127.0.0.1", "[::1]"):
            for port in (1, 12345, 65535):
                url = f"http://{host}:{port}"
                with self.subTest(url=url):
                    observed = self.accept(welcome_output(url))
                    self.assertEqual(observed, (WELCOME_NAMES - {DYNAMIC}) | {WELCOME + "/" + url})

    def test_noncanonical_or_remote_aliases_reject(self):
        urls = (
            "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://127.0.0.1:01",
            "http://127.0.0.1:-1", "http://127.0.0.1:", "http://127.0.0.1:1/",
            "http://127.0.0.1:1?x=1", "http://localhost:1", "http://127.0.0.2:1",
            "http://[::ffff:127.0.0.1]:1", "http://[::1]:0001", "http://[::1]:65536",
            "https://127.0.0.1:1", "http://user@127.0.0.1:1", "http://::1:1",
        )
        for url in urls:
            with self.subTest(url=url), self.assertRaises(RuntimeError):
                self.accept(welcome_output(url))

    def test_different_run_and_pass_ports_reject_before_normalization(self):
        raw = welcome_output().replace(
            f"--- PASS: {WELCOME}/http://127.0.0.1:12345".encode(),
            f"--- PASS: {WELCOME}/http://127.0.0.1:12346".encode(),
        )
        with self.assertRaises(RuntimeError):
            self.accept(raw)

    def test_literal_placeholder_cannot_hide_an_extra_pair(self):
        extra = f"=== RUN   {DYNAMIC}\n--- PASS: {DYNAMIC} (0.01s)\n".encode()
        with self.assertRaises(RuntimeError):
            self.accept(welcome_output() + extra)
        with self.assertRaises(RuntimeError):
            self.accept(test_output(WELCOME_NAMES))

    def test_missing_duplicate_and_multiple_aliases_reject(self):
        name = WELCOME + "/http://127.0.0.1:12345"
        run, passed = f"=== RUN   {name}\n".encode(), f"--- PASS: {name} (0.01s)\n".encode()
        other = WELCOME + "/http://[::1]:12345"
        bad = [welcome_output().replace(run, b""), welcome_output().replace(passed, b""),
               welcome_output() + run, welcome_output() + passed, welcome_output() + run + passed,
               welcome_output() + f"=== RUN   {other}\n--- PASS: {other} (0.01s)\n".encode()]
        for index, raw in enumerate(bad):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                self.accept(raw)

    def test_dynamic_acceptance_keeps_exit_stderr_and_failure_gates(self):
        for stderr, code in ((b"diagnostic", 0), (b"", 7)):
            with self.subTest(stderr=stderr, code=code), self.assertRaises(RuntimeError):
                self.accept(welcome_output(), stderr, code)
        for suffix in (f"--- SKIP: {WELCOME} (0.01s)\n", f"--- FAIL: {WELCOME} (0.01s)\n", "PASS\n"):
            with self.subTest(suffix=suffix), self.assertRaises(RuntimeError):
                self.accept(welcome_output() + suffix.encode())


class ProfileAdmissionTests(unittest.TestCase):
    def test_whole_profile_exact_budget_and_shortfall(self):
        for profile, seconds in (("local-state", 470), ("shell-contracts", 565)):
            self.assertEqual(sum(pilot.PROFILE_PHASES[profile].values()), seconds)
            for difference in (0, -0.001):
                commands = Mock(deadline=1000 + seconds + difference)
                with self.subTest(profile=profile, difference=difference), patch.object(pilot.time, "monotonic", return_value=1000):
                    if difference:
                        with self.assertRaises(RuntimeError):
                            pilot.run_profile(commands, [], profile, {}, {}, {})
                        commands.run.assert_not_called()
                    else:
                        pilot.admit_profile(commands, pilot.PROFILE_PHASES[profile])

    def test_rechecks_all_remaining_phases_before_next_launch(self):
        for profile, seconds, first in (("local-state", 470, 70), ("shell-contracts", 565, 170)):
            clock = [0.0]
            commands = Mock(deadline=float(seconds))
            def first_phase(*_args, **_kwargs):
                clock[0] = first + 0.001
                return []
            with self.subTest(profile=profile), patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]), \
                    patch.object(pilot, "run_selected_tests", side_effect=first_phase) as selected:
                with self.assertRaisesRegex(RuntimeError, "complete remaining"):
                    pilot.run_profile(commands, [], profile, {"imageprefs": {}}, {"openai": "a" * 64}, {})
                self.assertEqual(selected.call_count, 1)
                commands.run.assert_not_called()

    def test_local_payloads_keep_separate_bounds_and_final_reserve(self):
        suites = {name: {} for name in ("imageprefs", "custom", "autocomplete", "process", "clihelp")}
        commands, result = Mock(deadline=470.0), {}
        with patch.object(pilot.time, "monotonic", return_value=0), \
                patch.object(pilot, "run_selected_tests", return_value=[]) as selected:
            remaining = pilot.run_profile(commands, ["synthetic-user"], "local-state", suites, {}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["profile_admission_seconds"], 470)
        self.assertEqual([(call.args[2], call.args[4], call.args[5], call.kwargs) for call in selected.call_args_list], [
            ("/home/cli-compat/payload/imageprefs.test", "local-imageprefs", 70, {"dynamic_loopback": False}),
            ("/home/cli-compat/payload/custom.test", "local-custom", 70, {"dynamic_loopback": False}),
            ("/home/cli-compat/payload/autocomplete.test", "local-autocomplete", 70, {"dynamic_loopback": False}),
            ("/home/cli-compat/payload/process.test", "local-process", 170, {"dynamic_loopback": True}),
            ("/home/cli-compat/payload/clihelp.test", "local-clihelp", 70, {"dynamic_loopback": False}),
        ])
        commands.run.assert_not_called()

    def test_autocomplete_failure_stops_before_public_process_tests(self):
        suites = {name: {} for name in ("imageprefs", "custom", "autocomplete", "process", "clihelp")}
        commands, result = Mock(deadline=470.0), {}
        with patch.object(pilot.time, "monotonic", return_value=0), \
                patch.object(pilot, "run_selected_tests", side_effect=[[], [], RuntimeError("native rename failure")]) as selected:
            with self.assertRaisesRegex(RuntimeError, "native rename failure"):
                pilot.run_profile(commands, [], "local-state", suites, {}, result)
        self.assertEqual([call.args[4] for call in selected.call_args_list],
                         ["local-imageprefs", "local-custom", "local-autocomplete"])
        self.assertEqual(set(result["profile_results"]), {"imageprefs", "custom"})
        commands.run.assert_not_called()

    def test_native_selector_is_exactly_three_bash_leaves(self):
        self.assertEqual(pilot.test_names(pilot.NATIVE_CASES), NATIVE_NAMES)
        expressions = pilot.NATIVE_SELECTOR.split("/")
        self.assertEqual(len(expressions), 3)
        for name in NATIVE_NAMES:
            self.assertTrue(all(re.fullmatch(pattern, part) for pattern, part in zip(expressions, name.split("/"))))
        for name in ("TestMainNativeShellExtra/bash/installed_help_uses_command_name",
                     "TestMainNativeShell/zsh/installed_help_uses_command_name",
                     "TestMainNativeShell/bash/installed_help_uses_command_name_extra",
                     "TestMainNativeShell/bash/unreviewed"):
            self.assertFalse(all(re.fullmatch(pattern, part) for pattern, part in zip(expressions, name.split("/"))))

    def test_native_process_uses_prebuilt_cli_and_bounded_selected_invocation(self):
        responses = {
            "invocation": test_output(NATIVE_NAMES), "completion": completion_output(),
            "startup-login": receipt_output("bash-login"),
            "startup-nonlogin": receipt_output("bash-nonlogin"),
            "models": receipt_output("models-filter-sort-limit"),
        }
        commands = Mock(deadline=565.0)
        commands.run.side_effect = lambda label, _argv, _seconds: (responses[label], b"", 0)
        result = {"shell_prerequisites": {"bash": {"path": "/usr/bin/bash"}}}
        with patch.object(pilot.time, "monotonic", return_value=0):
            remaining = pilot.run_profile(commands, ["synthetic-user"], "shell-contracts", None,
                                          {"openai": "a" * 64}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["profile_admission_seconds"], 565)
        calls = commands.run.call_args_list
        self.assertEqual([(call.args[0], call.args[2]) for call in calls], [
            ("invocation", 170), ("completion", 75), ("startup-login", 130),
            ("startup-nonlogin", 130), ("models", 40),
        ])
        self.assertEqual(calls[0].args[1], [
            "synthetic-user", "OPENAI_CLI_REQUIRE_NATIVE_SHELLS=bash",
            "OPENAI_CLI_NATIVE_SHELL_BINARY=/home/cli-compat/payload/openai",
            "OPENAI_CLI_NATIVE_SHELL_SHA256=" + "a" * 64,
            "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s",
            "/home/cli-compat/payload/process.test", "-test.run=" + pilot.NATIVE_SELECTOR,
            "-test.timeout=150s", "-test.v",
        ])
        for call, timeout in zip(calls[1:], ("65s", "120s", "120s", "30s")):
            argv = call.args[1]
            self.assertEqual(argv[1:8], ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s",
                                       timeout, "/usr/bin/python3", "-I", "-B"])
            self.assertEqual(argv[9:11], ["/home/cli-compat/payload/openai", "/usr/bin/bash"])
        self.assertEqual(calls[2].args[1][-1], "login")
        self.assertEqual(calls[3].args[1][-1], "nonlogin")
        self.assertEqual(set(result["profile_results"]["invocation"]["tests"]), NATIVE_NAMES)


class DriverReceiptTests(unittest.TestCase):
    def test_startup_requires_bound_unchanged_system_sources(self):
        good = valid_receipt("bash-login")
        variants = []
        missing = deepcopy(good)
        del missing["system_sources"]
        variants.append(missing)
        changed = deepcopy(good)
        changed["system_sources_after"][0]["identity"]["inode"] += 1
        variants.append(changed)
        for field, value in (("sha256", "0" * 64), ("content_base64", "not base64"), ("present", 1)):
            bad = deepcopy(good)
            bad["system_sources"][0][field] = value
            bad["system_sources_after"] = deepcopy(bad["system_sources"])
            variants.append(bad)
        absent = deepcopy(good)
        absent["system_sources"][1]["content_base64"] = ""
        absent["system_sources_after"] = deepcopy(absent["system_sources"])
        variants.append(absent)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output("bash-login", receipt), b"", 0, {"bash-login"}, "bash-login")

    def test_completion_requires_all_seven_names_and_bash_version(self):
        self.assertEqual(pilot.COMPLETION_CASES, COMPLETION_NAMES)
        self.assertEqual(pilot.require_driver_pass(completion_output(), b"", 0, COMPLETION_NAMES),
                         {"cases": sorted(COMPLETION_NAMES)})
        first = sorted(COMPLETION_NAMES)[0]
        line = f"PASS: {first}\n".encode()
        bad = [completion_output().replace(line, b""), completion_output() + line,
               completion_output().replace(b"GNU bash, version synthetic\n", b""),
               completion_output().replace(b"GNU bash, version", b"other shell, version"),
               completion_output() + b"PASS: unexpected\n", completion_output() + b"diagnostic\n"]
        for index, raw in enumerate(bad):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(raw, b"", 0, COMPLETION_NAMES)

    def test_each_receipt_driver_requires_its_case_and_cleanup(self):
        for case in ("bash-login", "bash-nonlogin", "models-filter-sort-limit"):
            with self.subTest(case=case):
                receipt = pilot.require_driver_pass(receipt_output(case), b"", 0, {case}, case)
                self.assertEqual(receipt, valid_receipt(case))

    def test_driver_exit_stderr_and_encoding_fail_independently(self):
        fixtures = [(completion_output(), COMPLETION_NAMES, None),
                    (receipt_output("bash-login"), {"bash-login"}, "bash-login")]
        for raw, names, case in fixtures:
            for stderr, code in ((b"diagnostic", 0), (b"", 7)):
                with self.subTest(case=case, stderr=stderr, code=code), self.assertRaises(RuntimeError):
                    pilot.require_driver_pass(raw, stderr, code, names, case)
            with self.assertRaises(UnicodeDecodeError):
                pilot.require_driver_pass(b"\xff" + raw, b"", 0, names, case)

    def test_receipt_missing_duplicate_pass_and_extra_output_reject(self):
        case = "models-filter-sort-limit"
        raw = receipt_output(case)
        pass_line, receipt_line = raw.splitlines(keepends=True)
        variants = [pass_line, receipt_line, raw + pass_line, raw + receipt_line,
                    raw + b"unexpected\n", raw.replace(pass_line, b"PASS: other\n")]
        for index, value in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(value, b"", 0, {case}, case)

    def test_receipt_field_types_and_failure_values_reject(self):
        case = "bash-login"
        good = valid_receipt(case)
        variants = [None, [], {}, {**good, "case": "bash-nonlogin"}, {**good, "failures": ["synthetic failure"]}]
        for value in (1, False, True, "0", 0.0, None):
            variants.append({**good, "exit_code": value})
        for value in (False, 1, "true", None):
            variants.append({**good, "cleanup_complete": value})
        for receipt in variants:
            with self.subTest(receipt=receipt), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output(case, json.dumps(receipt)), b"", 0, {case}, case)

    def test_duplicate_receipt_keys_cannot_override_failure(self):
        good = json.dumps(valid_receipt("bash-login"), separators=(",", ":"))
        for field, duplicate in (('"exit_code":0', '"exit_code":1,"exit_code":0'),
                                 ('"cleanup_complete":true', '"cleanup_complete":false,"cleanup_complete":true')):
            with self.subTest(duplicate=duplicate), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output("bash-login", good.replace(field, duplicate, 1)),
                                          b"", 0, {"bash-login"}, "bash-login")

    def test_models_requires_exact_counts_output_and_eofs(self):
        case = "models-filter-sort-limit"
        good = valid_receipt(case)
        mutations = {
            "cli_call_count": (0, 2, True), "cli_exit_code": (1, False), "request_count": (0, 2),
            "accepted_request_count": (0, 2), "stdout_bytes": (120, 122), "stderr_bytes": (1,),
            "response_bytes": (5833,), "stdout_utf8": (good["stdout_utf8"].rstrip(), "{}\n"),
            "stdout_sha256": ("0" * 64,), "response_sha256": ("0" * 64,),
            "stdout_eof": (False, 1), "stderr_eof": (False,), "openai_settings_absent": (False,),
            "fixture_thread_joined": (False,),
        }
        for key, values in mutations.items():
            for value in values:
                with self.subTest(key=key, value=value), self.assertRaises(RuntimeError):
                    pilot.require_driver_pass(receipt_output(case, {**good, key: value}), b"", 0, {case}, case)

    def test_startup_requires_all_ordered_actions_and_checkpoints(self):
        case = "bash-login"
        mutations = []
        for key in ("setup_calls", "fresh_shells"):
            missing = valid_receipt(case)
            missing[key].pop()
            mutations.append(missing)
            duplicate = valid_receipt(case)
            duplicate[key][1] = duplicate[key][0]
            mutations.append(duplicate)
            reordered = valid_receipt(case)
            reordered[key].reverse()
            mutations.append(reordered)
        for index, receipt in enumerate(mutations):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_startup_requires_per_command_success_and_complete_output(self):
        case = "bash-nonlogin"
        for key, field, value in (
            ("setup_calls", "exit_code", 1), ("setup_calls", "exit_code", False),
            ("setup_calls", "stdout_eof", False), ("setup_calls", "stderr_eof", False),
            ("fresh_shells", "exit_code", 1), ("fresh_shells", "pty_eof", False),
            ("fresh_shells", "forced_cleanup", True),
        ):
            receipt = valid_receipt(case)
            receipt[key][-1][field] = value
            with self.subTest(key=key, field=field), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)
        for key, value in (("setup_call_count", 5), ("fresh_shell_count", 2),
                           ("expected_setup_calls", 5), ("expected_fresh_shells", 2),
                           ("setup_stdout_eof", False), ("setup_stderr_eof", False), ("pty_eof", False)):
            receipt = valid_receipt(case)
            receipt[key] = value
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_startup_rejects_missing_or_corrupted_raw_evidence(self):
        case = "bash-login"
        mutations = []
        for group, field in (("setup_calls", "stdout_base64"), ("setup_calls", "stderr_base64"),
                             ("fresh_shells", "pty_base64")):
            receipt = valid_receipt(case)
            del receipt[group][0][field]
            mutations.append(receipt)
        for group, field, value in (("setup_calls", "stderr_sha256", "0" * 64),
                                     ("fresh_shells", "pty_sha256", "0" * 64),
                                     ("fresh_shells", "pty_bytes", 0),
                                     ("fresh_shells", "pty_base64", "!!")):
            receipt = valid_receipt(case)
            receipt[group][0][field] = value
            mutations.append(receipt)
        missing_prompt = valid_receipt(case)
        missing_prompt["fresh_shells"][0].update(captured_fields("pty", b"no prompt\n"))
        mutations.append(missing_prompt)
        extra_stdout = valid_receipt(case)
        extra_stdout["setup_calls"][0].update(captured_fields("stdout", b"unexpected output\n"))
        mutations.append(extra_stdout)
        for index, receipt in enumerate(mutations):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                pilot.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)


class ProfileCLIAndCleanupTests(unittest.TestCase):
    def invoke_main(self, args):
        with patch.object(sys, "argv", ["check-wsl-pager.py", *args]), \
                patch.object(pilot.signal, "signal"), patch.object(pilot, "pilot", return_value=0) as run, \
                patch.object(sys, "stderr", io.StringIO()):
            result = pilot.main()
        return result, run

    def test_cli_routes_exact_payload_sets(self):
        code, run = self.invoke_main([
            "--profile", "local-state", "--binary", "custom.test", "--process-binary", "process.test",
            "--imageprefs-binary", "imageprefs.test", "--clihelp-binary", "clihelp.test",
            "--autocomplete-binary", "autocomplete.test",
        ])
        self.assertEqual(code, 0)
        run.assert_called_once_with(Path("custom.test"), False, Path("process.test"),
                                    profile="local-state", extra_payloads={
                                        "imageprefs.test": Path("imageprefs.test"), "clihelp.test": Path("clihelp.test"),
                                        "autocomplete.test": Path("autocomplete.test")})
        code, run = self.invoke_main(["--profile", "shell-contracts", "--process-binary", "process.test",
                                     "--cli-binary", "openai"])
        self.assertEqual(code, 0)
        run.assert_called_once_with(None, False, Path("process.test"), profile="shell-contracts",
                                    extra_payloads={"openai": Path("openai")})

    def test_cli_rejects_missing_mixed_and_extra_payloads(self):
        bad = [
            ["--profile", "shell-contracts", "--process-binary", "process.test"],
            ["--profile", "shell-contracts", "--cli-binary", "openai"],
            ["--profile", "shell-contracts", "--process-binary", "process.test", "--cli-binary", "openai", "--binary", "custom.test"],
            ["--profile", "local-state", "--binary", "custom.test", "--process-binary", "process.test"],
            ["--profile", "local-state", "--binary", "custom.test", "--process-binary", "process.test",
             "--imageprefs-binary", "imageprefs.test", "--clihelp-binary", "clihelp.test"],
            ["--profile", "shell-contracts", "--process-binary", "process.test", "--cli-binary", "openai",
             "--autocomplete-binary", "autocomplete.test"],
            ["--binary", "custom.test", "--cli-binary", "openai"],
            ["--binary", "custom.test", "--autocomplete-binary", "autocomplete.test"],
            ["--profile", "local-state", "--command-contracts"],
        ]
        for args in bad:
            with self.subTest(args=args), patch.object(pilot, "pilot") as run, \
                    patch.object(sys, "argv", ["check-wsl-pager.py", *args]), \
                    patch.object(pilot.signal, "signal"), patch.object(sys, "stderr", io.StringIO()):
                with self.assertRaises(SystemExit) as caught:
                    pilot.main()
                self.assertEqual(caught.exception.code, 2)
                run.assert_not_called()

    def test_direct_profile_rejects_invalid_payloads_before_native_access(self):
        invalid = [
            {"profile": "unknown"},
            {"profile": "shell-contracts", "command_contracts": True},
            {"profile": "shell-contracts", "process_binary": Path("process.test")},
            {"profile": "shell-contracts", "process_binary": Path("process.test"),
             "extra_payloads": {"openai": Path("openai"), "clihelp.test": Path("clihelp.test")}},
            {"profile": "local-state", "process_binary": Path("process.test")},
        ]
        for kwargs in invalid:
            with self.subTest(kwargs=kwargs), patch.object(pilot, "require_native_windows_process") as native:
                with self.assertRaises(RuntimeError):
                    pilot.pilot(None, **kwargs)
                native.assert_not_called()

    def test_local_profile_requires_autocomplete_before_native_access(self):
        with patch.object(pilot, "require_native_windows_process") as native:
            with self.assertRaisesRegex(RuntimeError, "exact payload set"):
                pilot.pilot(Path("custom.test"), process_binary=Path("process.test"), profile="local-state",
                            extra_payloads={"imageprefs.test": Path("imageprefs.test"), "clihelp.test": Path("clihelp.test")})
            native.assert_not_called()

    def test_successor_profiles_reject_command_contract_app_binary(self):
        cases = (
            ("local-state", Path("custom.test"), {
                "imageprefs.test": Path("imageprefs.test"), "clihelp.test": Path("clihelp.test"),
                "autocomplete.test": Path("autocomplete.test"),
            }, ["--binary", "custom.test", "--imageprefs-binary", "imageprefs.test",
                "--clihelp-binary", "clihelp.test", "--autocomplete-binary", "autocomplete.test"]),
            ("shell-contracts", None, {"openai": Path("openai")}, ["--cli-binary", "openai"]),
        )
        for profile, binary, extras, flags in cases:
            with self.subTest(profile=profile):
                args = ["--profile", profile, "--process-binary", "process.test",
                        "--app-binary", "app.test", *flags]
                with patch.object(sys, "argv", ["check-wsl-pager.py", *args]), \
                        patch.object(sys, "stderr", io.StringIO()), patch.object(pilot, "pilot") as run:
                    with self.assertRaises(SystemExit) as caught:
                        pilot.main()
                    self.assertEqual(caught.exception.code, 2)
                    run.assert_not_called()
                with patch.object(pilot, "require_native_windows_process") as native:
                    with self.assertRaisesRegex(RuntimeError, "Application lifecycle binaries"):
                        pilot.pilot(binary, process_binary=Path("process.test"), app_binary=Path("app.test"),
                                    profile=profile, extra_payloads=extras)
                    native.assert_not_called()

    def test_cleanup_cli_requires_no_payload_arguments(self):
        for profile in ("local-state", "shell-contracts"):
            with self.subTest(profile=profile):
                code, run = self.invoke_main(["--profile", profile, "--cleanup"])
                self.assertEqual(code, 0)
                run.assert_called_once_with(None, True, None, profile=profile, extra_payloads={})

    def test_cleanup_uses_owned_state_without_assets_or_inventories(self):
        for profile in ("local-state", "shell-contracts"):
            with self.subTest(profile=profile), tempfile.TemporaryDirectory() as directory:
                path = Path(directory)
                state = {"name": "owned", "directory": str(path), "import_attempted": False}
                (path / "state.json").write_text(json.dumps(state), encoding="utf-8")
                commands = Mock(deadline=1000.0, records=[])
                with patch.object(pilot, "require_native_windows_process", return_value={}), \
                        patch.object(pilot, "owned_paths", return_value=("owned", path)), \
                        patch.object(pilot, "remaining_deadline", return_value=1000.0), \
                        patch.object(pilot.time, "monotonic", return_value=0.0), \
                        patch.object(pilot, "Commands", return_value=commands), \
                        patch.object(pilot, "load_local_cases", side_effect=AssertionError("inventory accessed")) as local, \
                        patch.object(pilot, "load_command_cases", side_effect=AssertionError("inventory accessed")) as prior, \
                        patch.object(pilot, "require_linux_binary", side_effect=AssertionError("payload accessed")) as binary, \
                        patch.object(pilot, "digest", side_effect=AssertionError("asset accessed")) as digest, \
                        patch.object(pilot.Path, "is_file", side_effect=AssertionError("asset inspected")), \
                        patch.object(pilot.Path, "is_symlink", side_effect=AssertionError("asset inspected")), \
                        patch.object(pilot, "clean_distro") as clean, \
                        patch.dict(os.environ, {"SYSTEMROOT": str(path / "windows")}):
                    self.assertEqual(pilot.pilot(None, cleanup_only=True, profile=profile), 0)
                local.assert_not_called()
                prior.assert_not_called()
                binary.assert_not_called()
                digest.assert_not_called()
                commands.run.assert_not_called()
                commands.finish.assert_called_once()
                self.assertEqual(clean.call_args.args[2:], ("owned", state))
                result = json.loads((path / "cleanup.json").read_text())
                self.assertEqual(result["state"], "passed")
                self.assertEqual(result["selection"], profile)


if __name__ == "__main__":
    unittest.main(verbosity=2)
