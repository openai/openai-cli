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
sys.modules[spec.name] = pilot
spec.loader.exec_module(pilot)
receipts = pilot.load_shell_receipts()

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
    "escaped colon command", "quoted colon command", "unquoted colon command",
    "literal colon flag value", "separate colon command words",
}
NATIVE_SETUP_ROOT = "TestPickerNativeSetupContract"
NATIVE_SETUP_CASES = {NATIVE_SETUP_ROOT: [
    "rename_absent", "rename_existing", "install_absent", "install_existing", "seeded_noop",
    "seeded_remove", "missing_profile_cleanup", "pending_recovery", "replaced_profile", "canceled",
]}


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


def capability_output(contract="refused", errno="ENOSYS", receipt=None):
    if receipt is None:
        receipt = {"contract": contract, "errno": errno, "checks": 10}
    encoded = receipt if isinstance(receipt, str) else json.dumps(receipt)
    return (test_output(pilot.test_names(NATIVE_SETUP_CASES)).removesuffix(b"PASS\n")
            + f"PICKER_CAPABILITY {encoded}\nPASS\n".encode())


def first_use_receipt():
    output = b"OpenAI CLI\r\nSynthetic help\r\nUSAGE\r\n  openai [GLOBAL FLAGS] COMMAND\r\n\r\nFIRST_USE_STATUS:0\r\n"
    profiles = [{"path": "/home/cli-compat/" + name, "present": False}
                for name in (".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout")]
    result = {"account": {"name": "cli-compat", "uid": 2000, "home": "/home/cli-compat"},
              "call_count": 4, "cleanup_complete": True, "profiles_unchanged": True, "opt_out_preserved": True,
              "removed_environment_variables": ["CI"], "profiles_before": profiles, "profiles_after": deepcopy(profiles),
              "calls": []}
    for name, attempted in (("ci-ineligible", False), ("eligible-first", True),
                            ("eligible-repeat", True), ("opted-out", False)):
        stage = [".openai-picker-" + "A" * 26] if attempted else []
        result["calls"].append({"case": name, "attempted_setup": attempted, "exit_code": 0, "pty_eof": True,
                                "staging": {"created": stage, "deleted": stage.copy(),
                                            "sequence": [{"name": value, "mask": mask} for value in stage
                                                         for mask in (0x100, 0x200)]},
                                **captured_fields("pty", output)})
    return result


def refused_startup_receipt(case):
    receipt = valid_receipt(case.removesuffix("-refused"))
    receipt.update(case=case, setup_contract="refused", setup_call_count=7, expected_setup_calls=7,
                   installation_scope="Seeded existing state tests activation and cleanup, not installation.",
                   automatic_setup_scope="Not exercised: private HOME differs from the account home.",
                   seeded_existing_state={"contract": "seeded-existing-state", "exporter_exit_code": 0,
                                          "stdout_eof": True, "stderr_eof": True, "exporter_stdout_bytes": 128,
                                          "exporter_stderr_bytes": 0, "exporter_stdout_sha256": "a" * 64,
                                          "profile_count": 2, "script_count": 2})
    if case == "bash-nonlogin-refused":
        receipt["automatic_setup_scope"] = "Separate account-home admission is recorded in first_use_admission."
        receipt["first_use_admission"] = first_use_receipt()
    for field in ("original_profiles_unchanged", "seeded_profiles_unchanged", "recovery_evidence_unchanged",
                  "absent_profiles_preserved", "opt_out_created", "generated_scripts_removed",
                  "unselected_profile_unchanged"):
        receipt[field] = True
    receipt["setup_calls"] = []
    for action, status, error_format in (
        ("install-refused-text", 1, "text"), ("install-refused-json", 1, "json"),
        ("install-refused-again", 1, "text"), ("seeded-install-noop", 0, "text"),
        ("seeded-remove-refused-text", 1, "text"), ("seeded-remove-refused-json", 1, "json"),
        ("absent-profile-remove", 0, "text"),
    ):
        remove = "remove" in action
        if status:
            message = ("Could not finish Tab shortcut " + ("removal" if remove else "setup")
                       + ": this environment does not support the required atomic rename."
                       + (" Existing shortcuts may remain active." if remove else "")
                       + " Type openai images generate and press Enter to use the picker."
                       + " Some startup files or preferences may already have changed."
                       + " Keep any .openai-picker-recovery-* directories beside the startup file; inspect their original files before manual recovery."
                       + " Persistent setup needs a filesystem with atomic rename support.")
        else:
            message = ("Tab shortcut setup removed. Open a new terminal to finish." if remove else
                       "Tab setup saved for future terminals. Existing custom bindings are preserved.")
        raw = ((json.dumps({"message": message}) if error_format == "json" else message) + "\n").encode()
        receipt["setup_calls"].append({"action": action, "exit_code": status, "expected_exit_code": status,
                                        "format_error": error_format, "stdout_eof": True, "stderr_eof": True,
                                        **captured_fields("stdout", b""), **captured_fields("stderr", raw)})
    for item, checkpoint, active in zip(receipt["fresh_shells"],
                                         ("fresh-original", "fresh-seeded-existing", "fresh-after-refused-remove"),
                                         (False, True, True)):
        item.update(checkpoint=checkpoint, expected_active=active)
    return receipt


def resolved_plan(profile="local-state", setup_contract="supported"):
    if profile == "local-state":
        return pilot.resolve_profile(
            Path("custom.test"), process_binary=Path("process.test"), profile=profile,
            extra_payloads={name + ".test": Path(name + ".test")
                            for name in ("imageprefs", "clihelp", "autocomplete")},
            setup_contract=setup_contract)
    extras = {"openai": Path("openai")}
    if setup_contract == "refused":
        extras["autocomplete.test"] = Path("autocomplete.test")
    return pilot.resolve_profile(process_binary=Path("process.test"), profile=profile,
                                 extra_payloads=extras, setup_contract=setup_contract)


def local_phase_response(phase, contract="supported", errno=None):
    raw = test_output(pilot.test_names(phase.cases))
    raw = raw.replace(DYNAMIC.encode(), (WELCOME + "/http://127.0.0.1:12345").encode())
    if NATIVE_SETUP_ROOT in phase.cases:
        receipt = {"contract": contract, "errno": errno if errno is not None else (
            "" if contract == "supported" else "ENOSYS"), "checks": 10}
        raw = raw.removesuffix(b"PASS\n") + f"PICKER_CAPABILITY {json.dumps(receipt)}\nPASS\n".encode()
    return raw, b"", 0


def phase_named(plan, label):
    return next(phase for phase in plan.phases if phase.label == label)


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
        expected = {"imageprefs": (9, 21, 30), "custom": (12, 17, 29),
                    "autocomplete": (5, 54, 59), "process": (29, 118, 147), "clihelp": (5, 1, 6)}
        total = [0, 0, 0]
        for package, counts in expected.items():
            cases = suites[package]
            observed = (len(cases), sum(map(len, cases.values())), len(pilot.test_names(cases)))
            self.assertEqual(observed, counts)
            total = [left + right for left, right in zip(total, observed)]
        self.assertEqual(total, [60, 211, 271])
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
            "TestHelpInvocationKeepsDifferentExecutable": [],
            "TestHelpInvocationKeepsLocalPathWithImplicitLookup": [],
        })
        self.assertEqual(set(suites["autocomplete"]), {
            "TestPickerRenameNoReplace", "TestPickerInstallRoundTrip", "TestPickerRemoveRestoresAbsentProfile",
            "TestPickerRenameCapabilityClassification", NATIVE_SETUP_ROOT,
        })
        self.assertEqual(suites["autocomplete"][NATIVE_SETUP_ROOT], NATIVE_SETUP_CASES[NATIVE_SETUP_ROOT])
        self.assertEqual(suites["autocomplete"]["TestPickerRenameCapabilityClassification"], [
            "success", "ENOSYS", "EOPNOTSUPP", "EINVAL", "EACCES", "EPERM", "EROFS", "EBADF",
            "wrapped_unrelated_EINVAL", "EEXIST", "ENOENT", "EXDEV", "canceled", "deadline",
            "wrapped_unrelated_operation",
        ])
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


class CapabilityReceiptTests(unittest.TestCase):
    def test_exact_supported_and_refused_receipts(self):
        for contract, errno in (("supported", ""), ("refused", "ENOSYS"), ("refused", "EOPNOTSUPP"), ("refused", "EINVAL")):
            with self.subTest(contract=contract, errno=errno):
                self.assertEqual(pilot.require_capability_receipt(capability_output(contract, errno), contract),
                                 {"contract": contract, "errno": errno, "checks": 10})

    def test_receipt_rejects_wrong_contract_errno_count_and_shape(self):
        good = {"contract": "refused", "errno": "ENOSYS", "checks": 10}
        bad = [None, [], {}, {**good, "extra": True}]
        for key, values in {
            "contract": ("supported", "auto", "", None, False),
            "errno": ("", "EACCES", "EPERM", "EEXIST", "EROFS", "EBADF", "EXDEV", "function not implemented", None, []),
            "checks": (0, 9, 11, True, False, "10", 10.0, None),
        }.items():
            bad.extend({**good, key: value} for value in values)
        for receipt in bad:
            with self.subTest(receipt=receipt), self.assertRaises(RuntimeError):
                pilot.require_capability_receipt(capability_output(receipt=json.dumps(receipt)), "refused")
        for contract, errno in (("supported", "ENOSYS"), ("supported", "EOPNOTSUPP"), ("supported", "EINVAL"), ("auto", "")):
            with self.subTest(contract=contract, errno=errno), self.assertRaises(RuntimeError):
                pilot.require_capability_receipt(capability_output(contract, errno), contract)

    def test_missing_duplicate_and_malformed_receipts_reject(self):
        good = capability_output()
        line = next(line for line in good.splitlines(keepends=True) if line.startswith(b"PICKER_CAPABILITY "))
        variants = [good.replace(line, b""), good + line,
                    good.replace(line, b"PICKER_CAPABILITY not-json\n"),
                    good.replace(line, b'PICKER_CAPABILITY {"contract":"supported","contract":"refused","errno":"ENOSYS","checks":10}\n'),
                    good.replace(line, b'PICKER_CAPABILITY {"contract":"refused","errno":"EACCES","errno":"ENOSYS","checks":10}\n'),
                    good.replace(line, b'PICKER_CAPABILITY {"contract":"refused","errno":"ENOSYS","checks":0,"checks":10}\n')]
        for index, output in enumerate(variants):
            with self.subTest(index=index), self.assertRaises((RuntimeError, ValueError)):
                pilot.require_capability_receipt(output, "refused")

    def test_planned_native_phase_requires_execution_and_receipt_before_acceptance(self):
        for contract, errno in (("supported", ""), ("refused", "ENOSYS"),
                                ("refused", "EOPNOTSUPP"), ("refused", "EINVAL")):
            phase = phase_named(resolved_plan(setup_contract=contract), "local-autocomplete")
            with self.subTest(contract=contract, errno=errno):
                entry = phase.validate(*local_phase_response(phase, contract, errno))
                self.assertEqual(set(entry["tests"]), pilot.test_names(phase.cases))
                self.assertEqual(entry["native_capability"],
                                 {"contract": contract, "errno": errno, "checks": 10})
                self.assertEqual(phase.arguments({}, {}), [
                    "OPENAI_CLI_PICKER_SETUP_CONTRACT=" + contract,
                    "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "60s",
                    "/home/cli-compat/payload/autocomplete.test",
                    "-test.run=^(" + "|".join(phase.cases) + ")$", "-test.timeout=45s", "-test.v"])

    def test_receipt_never_overrides_failed_skipped_or_incomplete_tests(self):
        phase = phase_named(resolved_plan(setup_contract="refused"), "local-autocomplete")
        good = local_phase_response(phase, "refused")[0]
        child = NATIVE_SETUP_ROOT + "/seeded_remove"
        passed = f"--- PASS: {child} (0.01s)\n".encode()
        variants = [(good, b"arbitrary failure", 0), (good, b"", 1),
                    (good.replace(passed, f"--- SKIP: {child} (0.01s)\n".encode()), b"", 0),
                    (good.replace(passed, f"--- FAIL: {child} (0.01s)\n".encode()), b"", 0),
                    (good.replace(passed, b""), b"", 0),
                    (good.replace(f"=== RUN   {child}\n".encode(), b""), b"", 0),
                    (good + passed, b"", 0),
                    (test_output(pilot.test_names(phase.cases)), b"", 0),
                    local_phase_response(phase, "refused", "EACCES")]
        for index, response in enumerate(variants):
            accepted = {}
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                accepted["entry"] = phase.validate(*response)
            self.assertEqual(accepted, {}, "failed native evidence must not populate an accepted result")


class ProfileAdmissionTests(unittest.TestCase):
    def local_commands(self, contract="supported", deadline=470.0):
        plan = resolved_plan(setup_contract=contract)
        commands = Mock(deadline=deadline)
        commands.run.side_effect = lambda label, _argv, _seconds: local_phase_response(
            phase_named(plan, label), contract)
        return plan, commands

    def shell_commands(self, contract="supported", deadline=565.0):
        plan = resolved_plan("shell-contracts", contract)
        suffix = "-refused" if contract == "refused" else ""
        responses = {"invocation": test_output(NATIVE_NAMES), "completion": completion_output(),
                     "models": receipt_output("models-filter-sort-limit")}
        for mode in ("login", "nonlogin"):
            case = "bash-" + mode + suffix
            responses["startup-" + mode] = receipt_output(
                case, refused_startup_receipt(case) if suffix else valid_receipt(case))
        commands = Mock(deadline=deadline)
        commands.run.side_effect = lambda label, _argv, _seconds: (responses[label], b"", 0)
        return plan, commands

    def test_whole_profile_exact_budget_and_shortfall(self):
        for profile, seconds in (("local-state", 470), ("shell-contracts", 565)):
            plan = resolved_plan(profile)
            self.assertEqual(sum(plan.budgets.values()), seconds)
            for difference in (0, -0.001):
                commands = Mock(deadline=1000 + seconds + difference)
                with self.subTest(profile=profile, difference=difference), \
                        patch.object(pilot.time, "monotonic", return_value=1000):
                    if difference:
                        with self.assertRaises(RuntimeError):
                            pilot.run_profile(commands, [], plan, {}, {})
                        commands.run.assert_not_called()
                    else:
                        pilot.admit_profile(commands, plan.budgets)

    def test_expired_first_phase_stops_before_next_launch(self):
        for profile, seconds, first in (("local-state", 470, 70), ("shell-contracts", 565, 170)):
            plan, commands = self.local_commands() if profile == "local-state" else self.shell_commands()
            response = commands.run.side_effect
            clock = [0.0]
            def first_phase(*args):
                clock[0] = first + 0.001
                return response(*args)
            commands.run.side_effect = first_phase
            result = {"shell_prerequisites": {"bash": {"path": "/usr/bin/bash"}}}
            with self.subTest(profile=profile), patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
                message = "shared time budget" if profile == "local-state" else "complete remaining"
                with self.assertRaisesRegex(RuntimeError, message):
                    pilot.run_profile(commands, [], plan, {"openai": "a" * 64}, result)
            self.assertEqual(commands.run.call_count, 1)

    def test_local_payloads_keep_separate_bounds_and_final_reserve(self):
        suites, _ = pilot.load_local_cases()
        plan, commands = self.local_commands()
        result = {}
        with patch.object(pilot.time, "monotonic", return_value=0):
            remaining = pilot.run_profile(commands, ["synthetic-user"], plan, {}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["profile_admission_seconds"], 470)
        expected = [
            ("imageprefs", "local-imageprefs", 70), ("clihelp", "local-clihelp", 70),
            ("custom", "local-custom-independent", 70), ("process", "local-process-independent", 170),
            ("autocomplete", "local-autocomplete", 70), ("custom", "local-custom", 70),
            ("process", "local-process", 170),
        ]
        self.assertEqual([(call.args[0], call.args[2]) for call in commands.run.call_args_list],
                         [(label, seconds) for _, label, seconds in expected])
        for call, (payload, label, seconds) in zip(commands.run.call_args_list, expected):
            cases = phase_named(plan, label).cases
            self.assertEqual(call.args[1], [
                "synthetic-user", "OPENAI_CLI_PICKER_SETUP_CONTRACT=supported",
                "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s",
                "160s" if seconds == 170 else "60s", "/home/cli-compat/payload/" + payload + ".test",
                "-test.run=^(" + "|".join(cases) + ")$",
                "-test.timeout=150s" if seconds == 170 else "-test.timeout=45s", "-test.v"])
        groups = [phase_named(plan, label).cases for label in ("local-process-independent", "local-process")]
        self.assertEqual([len(group) for group in groups], [23, 6])
        self.assertEqual([len(pilot.test_names(group)) for group in groups], [130, 17])
        self.assertFalse(set(groups[0]) & set(groups[1]))
        self.assertEqual({root: list(children) for root, children in {**groups[0], **groups[1]}.items()}, suites["process"])
        self.assertEqual(set(groups[0]), pilot.INDEPENDENT_PROCESS_ROOTS)
        self.assertIn("TestMainPickerShellSetupRefusesUnsafeConfigurationAncestry", groups[0])
        self.assertIn("TestMainPickerShellSetupReadOnlyModesDoNotWrite", groups[0])
        self.assertIn(WELCOME, groups[0])
        custom = [phase_named(plan, label).cases for label in ("local-custom-independent", "local-custom")]
        self.assertEqual([len(group) for group in custom], [11, 1])
        self.assertEqual([len(pilot.test_names(group)) for group in custom], [26, 3])
        self.assertFalse(set(custom[0]) & set(custom[1]))
        self.assertEqual({root: list(children) for root, children in {**custom[0], **custom[1]}.items()}, suites["custom"])
        self.assertEqual(sum(len(item["tests"]) for item in result["profile_results"].values()), 271)
        self.assertTrue(result["positive_installation_tests_executed"])
        self.assertEqual(result["positive_installation_tests_not_selected"], [])
        self.assertEqual(commands.deadline, 470)

    def test_autocomplete_failure_retains_independent_process_results(self):
        plan, commands = self.local_commands()
        result = {}
        response = commands.run.side_effect
        def fail_autocomplete(label, *args):
            if label == "local-autocomplete":
                raise RuntimeError("native rename failure")
            return response(label, *args)
        commands.run.side_effect = fail_autocomplete
        with patch.object(pilot.time, "monotonic", return_value=0):
            with self.assertRaisesRegex(RuntimeError, "native rename failure"):
                pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual([call.args[0] for call in commands.run.call_args_list],
                         ["local-imageprefs", "local-clihelp", "local-custom-independent",
                          "local-process-independent", "local-autocomplete"])
        self.assertEqual(set(result["profile_results"]),
                         {"imageprefs", "clihelp", "custom-independent", "process-independent"})
        self.assertEqual(len(result["profile_results"]["process-independent"]["tests"]), 130)
        self.assertEqual(len(result["profile_results"]["custom-independent"]["tests"]), 26)
        self.assertEqual(len(result["profile_results"]["clihelp"]["tests"]), 6)
        self.assertNotIn("positive_installation_tests_executed", result)
        self.assertEqual(commands.deadline, 470)

    def test_invalid_phase_receipt_stops_later_phases(self):
        plan, commands = self.local_commands("refused")
        result = {}
        response = commands.run.side_effect
        def invalid_capability(label, *args):
            if label == "local-autocomplete":
                return local_phase_response(phase_named(plan, label), "refused", "EACCES")
            return response(label, *args)
        commands.run.side_effect = invalid_capability
        with patch.object(pilot.time, "monotonic", return_value=0):
            with self.assertRaisesRegex(RuntimeError, "capability receipt"):
                pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual([call.args[0] for call in commands.run.call_args_list],
                         ["local-imageprefs", "local-clihelp", "local-custom-independent",
                          "local-process-independent", "local-autocomplete"])
        self.assertEqual(set(result["profile_results"]),
                         {"imageprefs", "clihelp", "custom-independent", "process-independent"})
        self.assertNotIn("positive_installation_tests_executed", result)
        self.assertEqual(commands.deadline, 470)

    def test_refused_profile_records_omitted_positive_tests_separately(self):
        suites, _ = pilot.load_local_cases()
        plan, commands = self.local_commands("refused")
        result = {}
        with patch.object(pilot.time, "monotonic", return_value=0):
            remaining = pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["setup_contract"], "refused")
        self.assertFalse(result["positive_installation_tests_executed"])
        self.assertEqual(result["profile_results"]["autocomplete"]["native_capability"],
                         {"contract": "refused", "errno": "ENOSYS", "checks": 10})
        self.assertEqual([call.args[0] for call in commands.run.call_args_list], [
            "local-imageprefs", "local-clihelp", "local-custom-independent", "local-process-independent",
            "local-autocomplete", "local-custom", "local-process"])
        for call in commands.run.call_args_list:
            self.assertIn("OPENAI_CLI_PICKER_SETUP_CONTRACT=refused", call.args[1])
        groups = {phase.label: phase.cases for phase in plan.phases}
        self.assertEqual(set(groups["local-autocomplete"]),
                         {NATIVE_SETUP_ROOT, "TestPickerRenameCapabilityClassification"})
        self.assertEqual(set(groups["local-process"]), {"TestMainPickerShellSetupNativeContract"})
        self.assertEqual(len(pilot.test_names(groups["local-autocomplete"])), 27)
        self.assertEqual(len(pilot.test_names(groups["local-process"])), 5)
        executed = set().union(*(set(entry["tests"]) for entry in result["profile_results"].values()))
        omitted = set(result["positive_installation_tests_not_selected"])
        inventory = set().union(*(pilot.test_names(cases) for cases in suites.values()))
        self.assertEqual(len(executed), 227)
        self.assertEqual(len(omitted), 44)
        self.assertFalse(executed & omitted)
        self.assertEqual(executed | omitted,
                         (inventory - {DYNAMIC}) | {WELCOME + "/http://127.0.0.1:12345"})
        self.assertEqual(omitted, pilot.test_names({root: suites["autocomplete"][root]
                                                  for root in pilot.SUPPORTED_AUTOCOMPLETE_ROOTS})
                         | pilot.test_names({root: children for root, children in suites["process"].items()
                                             if root not in pilot.INDEPENDENT_PROCESS_ROOTS
                                             and root != "TestMainPickerShellSetupNativeContract"}))
        self.assertEqual(commands.deadline, 470)

    def test_independent_process_failure_restores_deadline_and_stops_profile(self):
        plan, commands = self.local_commands()
        result = {}
        response = commands.run.side_effect
        def fail_process(label, *args):
            if label == "local-process-independent":
                raise RuntimeError("process failure")
            return response(label, *args)
        commands.run.side_effect = fail_process
        with patch.object(pilot.time, "monotonic", return_value=0):
            with self.assertRaisesRegex(RuntimeError, "process failure"):
                pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(commands.run.call_count, 4)
        self.assertEqual(set(result["profile_results"]), {"imageprefs", "clihelp", "custom-independent"})
        self.assertEqual(commands.deadline, 470)

    def test_process_groups_share_original_budget_across_autocomplete(self):
        plan, commands = self.local_commands()
        result, clock, observed = {}, [0.0], []
        response = commands.run.side_effect
        def run(label, *args):
            if label == "local-process-independent":
                observed.append((label, commands.deadline))
                clock[0] += 37
            elif label == "local-autocomplete":
                clock[0] += 69
            elif label == "local-process":
                observed.append((label, commands.deadline))
                clock[0] += 21
            return response(label, *args)
        commands.run.side_effect = run
        with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(observed, [("local-process-independent", 170), ("local-process", 239)])
        self.assertEqual(commands.deadline, 470)
        self.assertEqual(result["profile_admission_seconds"], 470)

    def test_custom_groups_share_original_budget_across_other_phases(self):
        plan, commands = self.local_commands()
        result, clock, observed = {}, [0.0], []
        response = commands.run.side_effect
        elapsed = {"local-custom-independent": 11, "local-process-independent": 37,
                   "local-autocomplete": 69, "local-custom": 9}
        def run(label, *args):
            if label in {"local-custom-independent", "local-custom"}:
                observed.append((label, commands.deadline))
            clock[0] += elapsed.get(label, 0)
            return response(label, *args)
        commands.run.side_effect = run
        with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(observed, [("local-custom-independent", 70), ("local-custom", 176)])
        self.assertEqual(commands.deadline, 470)
        self.assertEqual(result["profile_admission_seconds"], 470)

    def test_custom_exhaustion_stops_before_dependent_mutations(self):
        plan, commands = self.local_commands()
        result, clock = {}, [0.0]
        response = commands.run.side_effect
        def run(label, *args):
            if label == "local-custom-independent":
                clock[0] = 70
            return response(label, *args)
        commands.run.side_effect = run
        with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            with self.assertRaisesRegex(RuntimeError, "shared time budget"):
                pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(commands.run.call_count, 3)
        self.assertEqual(set(result["profile_results"]), {"imageprefs", "clihelp"})
        self.assertEqual(commands.deadline, 470)

    def test_late_independent_result_cannot_be_accepted(self):
        plan, commands = self.local_commands()
        result, clock = {}, [0.0]
        response = commands.run.side_effect
        def run(label, *args):
            if label == "local-process-independent":
                clock[0] = 170
            return response(label, *args)
        commands.run.side_effect = run
        with patch.object(pilot.time, "monotonic", side_effect=lambda: clock[0]):
            with self.assertRaisesRegex(RuntimeError, "shared time budget"):
                pilot.run_profile(commands, [], plan, {}, result)
        self.assertEqual(commands.run.call_count, 4)
        self.assertNotIn("process-independent", result["profile_results"])
        self.assertEqual(commands.deadline, 470)

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
        plan, commands = self.shell_commands()
        result = {"shell_prerequisites": {"bash": {"path": "/usr/bin/bash"}}}
        with patch.object(pilot.time, "monotonic", return_value=0):
            remaining = pilot.run_profile(commands, ["synthetic-user"], plan, {"openai": "a" * 64}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["profile_admission_seconds"], 565)
        calls = commands.run.call_args_list
        self.assertEqual([(call.args[0], call.args[2]) for call in calls], [
            ("invocation", 170), ("completion", 75), ("startup-login", 130),
            ("startup-nonlogin", 130), ("models", 40)])
        self.assertEqual(calls[0].args[1], [
            "synthetic-user", "OPENAI_CLI_REQUIRE_NATIVE_SHELLS=bash",
            "OPENAI_CLI_NATIVE_SHELL_BINARY=/home/cli-compat/payload/openai",
            "OPENAI_CLI_NATIVE_SHELL_SHA256=" + "a" * 64,
            "/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s",
            "/home/cli-compat/payload/process.test", "-test.run=" + pilot.NATIVE_SELECTOR,
            "-test.timeout=150s", "-test.v"])
        for call, timeout in zip(calls[1:], ("65s", "120s", "120s", "30s")):
            argv = call.args[1]
            self.assertEqual(argv[1:8], ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s",
                                       timeout, "/usr/bin/python3", "-I", "-B"])
            self.assertEqual(argv[9:11], ["/home/cli-compat/payload/openai", "/usr/bin/bash"])
        self.assertEqual(calls[2].args[1][-1], "login")
        self.assertEqual(calls[3].args[1][-1], "nonlogin")
        self.assertEqual(set(result["profile_results"]["invocation"]["tests"]), NATIVE_NAMES)

    def test_refused_shell_profile_routes_exporter_to_both_startup_modes(self):
        plan, commands = self.shell_commands("refused")
        result = {"shell_prerequisites": {"bash": {"path": "/usr/bin/bash"}}}
        with patch.object(pilot.time, "monotonic", return_value=0):
            remaining = pilot.run_profile(commands, ["synthetic-user"], plan,
                                          {"openai": "a" * 64, "autocomplete.test": "b" * 64}, result)
        self.assertEqual(remaining, {"final": 20})
        self.assertEqual(result["profile_admission_seconds"], 565)
        self.assertEqual(result["setup_contract"], "refused")
        calls = commands.run.call_args_list
        self.assertEqual([(call.args[0], call.args[2]) for call in calls], [
            ("invocation", 170), ("completion", 75), ("startup-login", 130),
            ("startup-nonlogin", 130), ("models", 40)])
        for call, mode in zip(calls[2:4], ("login", "nonlogin")):
            expected = [mode, "--setup-contract", "refused", "--autocomplete-test-binary",
                        "/home/cli-compat/payload/autocomplete.test"]
            if mode == "nonlogin":
                expected.append("--first-use-admission")
            self.assertEqual(call.args[1][-len(expected):], expected)
            self.assertEqual(result["profile_results"]["startup-" + mode]["case"], "bash-" + mode + "-refused")
        for call in (calls[0], calls[1], calls[4]):
            self.assertNotIn("--setup-contract", call.args[1])
            self.assertNotIn("/home/cli-compat/payload/autocomplete.test", call.args[1])


class DriverReceiptTests(unittest.TestCase):
    def test_first_use_rejects_missing_help_content(self):
        for removed in (b"OpenAI CLI", b"USAGE\r\n  openai [GLOBAL FLAGS] COMMAND\r\n"):
            bad = first_use_receipt()
            for call in bad["calls"]:
                raw = base64.b64decode(call["pty_base64"]).replace(removed, b"")
                call.update(captured_fields("pty", raw))
            with self.subTest(removed=removed), self.assertRaises(RuntimeError):
                receipts.require_first_use_receipt(bad)

    def test_first_use_requires_two_attempts_and_two_negative_controls(self):
        receipts.require_first_use_receipt(first_use_receipt())
        for index in range(4):
            for fault in ("no-stage", "stage", "unremoved-stage", "wrong-status", "missing-eof"):
                bad = first_use_receipt()
                call = bad["calls"][index]
                if fault == "no-stage":
                    call["staging"] = {"created": [], "deleted": [], "sequence": []}
                elif fault == "stage":
                    call["staging"] = {"created": [".openai-picker-" + "B" * 26],
                                       "deleted": [".openai-picker-" + "B" * 26],
                                       "sequence": [{"name": ".openai-picker-" + "B" * 26, "mask": mask}
                                                    for mask in (0x100, 0x200)]}
                elif fault == "unremoved-stage":
                    call["staging"]["created"].append(".openai-picker-" + "C" * 26)
                elif fault == "wrong-status":
                    call["exit_code"] = 1
                else:
                    call["pty_eof"] = False
                if (fault == "no-stage" and index in (0, 3)) or (fault == "stage" and index in (1, 2)):
                    continue
                with self.subTest(index=index, fault=fault), self.assertRaises(RuntimeError):
                    receipts.require_first_use_receipt(bad)

    def test_first_use_rejects_output_changes_and_incomplete_source_identity(self):
        variants = []
        for index in (1, 2, 3):
            bad = first_use_receipt()
            raw = base64.b64decode(bad["calls"][index]["pty_base64"])
            bad["calls"][index].update(captured_fields("pty", b"unexpected setup diagnostic\r\n" + raw))
            variants.append(bad)
        for field in ("cleanup_complete", "profiles_unchanged", "opt_out_preserved"):
            bad = first_use_receipt()
            bad[field] = False
            variants.append(bad)
        bad = first_use_receipt()
        bad["profiles_after"][0]["present"] = True
        variants.append(bad)
        bad = first_use_receipt()
        bad["profiles_before"][0] = bad["profiles_after"][0] = {"path": "/home/cli-compat/.bashrc"}
        variants.append(bad)
        bad = first_use_receipt()
        bad["account"]["home"] = "/private/home"
        variants.append(bad)
        bad = first_use_receipt()
        bad["removed_environment_variables"].append("HOME")
        variants.append(bad)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_first_use_receipt(receipt)

    def test_refused_nonlogin_requires_first_use_evidence(self):
        case = "bash-nonlogin-refused"
        for value in (None, {}, first_use_receipt() | {"calls": []}):
            receipt = refused_startup_receipt(case)
            receipt["first_use_admission"] = value
            with self.subTest(value=value), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_first_use_rejects_reordered_duplicate_and_incomplete_staging_events(self):
        for fault in ("reverse", "duplicate", "incomplete"):
            receipt = first_use_receipt()
            sequence = receipt["calls"][1]["staging"]["sequence"]
            if fault == "reverse":
                sequence.reverse()
            elif fault == "duplicate":
                sequence.extend(sequence.copy())
            else:
                sequence.pop()
            with self.subTest(fault=fault), self.assertRaises(RuntimeError):
                receipts.require_first_use_receipt(receipt)

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
                receipts.require_driver_pass(receipt_output("bash-login", receipt), b"", 0, {"bash-login"}, "bash-login")

    def test_completion_requires_all_twelve_names_and_bash_version(self):
        self.assertEqual(pilot.COMPLETION_CASES, COMPLETION_NAMES)
        self.assertEqual(receipts.require_driver_pass(completion_output(), b"", 0, COMPLETION_NAMES),
                         {"cases": sorted(COMPLETION_NAMES)})
        first = sorted(COMPLETION_NAMES)[0]
        line = f"PASS: {first}\n".encode()
        bad = [completion_output().replace(line, b""), completion_output() + line,
               completion_output().replace(b"GNU bash, version synthetic\n", b""),
               completion_output().replace(b"GNU bash, version", b"other shell, version"),
               completion_output() + b"PASS: unexpected\n", completion_output() + b"diagnostic\n"]
        for index, raw in enumerate(bad):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(raw, b"", 0, COMPLETION_NAMES)

    def test_each_receipt_driver_requires_its_case_and_cleanup(self):
        for case in ("bash-login", "bash-nonlogin", "models-filter-sort-limit"):
            with self.subTest(case=case):
                receipt = receipts.require_driver_pass(receipt_output(case), b"", 0, {case}, case)
                self.assertEqual(receipt, valid_receipt(case))

    def test_refused_receipts_keep_their_distinct_state_contract(self):
        for case in ("bash-login-refused", "bash-nonlogin-refused"):
            receipt = refused_startup_receipt(case)
            with self.subTest(case=case):
                self.assertEqual(receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case), receipt)
                self.assertEqual([item["exit_code"] for item in receipt["setup_calls"]], [1, 1, 1, 0, 1, 1, 0])
                self.assertEqual([item["expected_active"] for item in receipt["fresh_shells"]], [False, True, True])
                with self.assertRaises(RuntimeError):
                    receipts.require_driver_pass(receipt_output(case, valid_receipt(case)), b"", 0, {case}, case)

    def test_refused_receipts_reject_changed_actions_and_exit_contracts(self):
        case = "bash-login-refused"
        variants = []
        for group in ("setup_calls", "fresh_shells"):
            missing = refused_startup_receipt(case)
            missing[group].pop()
            variants.append(missing)
            duplicate = refused_startup_receipt(case)
            duplicate[group][1] = duplicate[group][0]
            variants.append(duplicate)
            reordered = refused_startup_receipt(case)
            reordered[group].reverse()
            variants.append(reordered)
        for index in range(7):
            for field in ("exit_code", "expected_exit_code"):
                for value in (1 - refused_startup_receipt(case)["setup_calls"][index][field], True, "1"):
                    bad = refused_startup_receipt(case)
                    bad["setup_calls"][index][field] = value
                    variants.append(bad)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_refused_receipts_reject_changed_formats_and_diagnostics(self):
        case = "bash-nonlogin-refused"
        variants = []
        for index in range(7):
            bad = refused_startup_receipt(case)
            item = bad["setup_calls"][index]
            item["format_error"] = "text" if item["format_error"] == "json" else "json"
            variants.append(bad)
            bad = refused_startup_receipt(case)
            bad["setup_calls"][index].update(captured_fields("stderr", b"arbitrary failure\n"))
            variants.append(bad)
            bad = refused_startup_receipt(case)
            bad["setup_calls"][index].update(captured_fields("stdout", b"unexpected success\n"))
            variants.append(bad)
        for raw in (b'{}\n', b'{"message":"different failure"}\n', b'[]\n', b'not-json\n'):
            bad = refused_startup_receipt(case)
            bad["setup_calls"][1].update(captured_fields("stderr", raw))
            variants.append(bad)
        duplicate = refused_startup_receipt(case)
        raw = base64.b64decode(duplicate["setup_calls"][1]["stderr_base64"])
        raw = raw.replace(b'{"message":', b'{"message":"arbitrary failure","message":', 1)
        duplicate["setup_calls"][1].update(captured_fields("stderr", raw))
        variants.append(duplicate)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises((RuntimeError, ValueError)):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_refused_receipts_require_preservation_and_evidence_limits(self):
        case = "bash-login-refused"
        variants = []
        for field in ("original_profiles_unchanged", "seeded_profiles_unchanged", "recovery_evidence_unchanged",
                      "absent_profiles_preserved", "opt_out_created", "generated_scripts_removed",
                      "unselected_profile_unchanged"):
            for value in (False, 1, None):
                bad = refused_startup_receipt(case)
                bad[field] = value
                variants.append(bad)
        for field in ("installation_scope", "automatic_setup_scope", "setup_contract"):
            for value in (None, "", "successful installation"):
                bad = refused_startup_receipt(case)
                bad[field] = value
                variants.append(bad)
        for index in range(3):
            for value in (not refused_startup_receipt(case)["fresh_shells"][index]["expected_active"], int(index != 0), None):
                bad = refused_startup_receipt(case)
                bad["fresh_shells"][index]["expected_active"] = value
                variants.append(bad)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_refused_receipts_require_bounded_successful_fixture_export(self):
        case = "bash-login-refused"
        mutations = {
            "contract": ("installed", None), "exporter_exit_code": (1, False, "0"),
            "exporter_stderr_bytes": (1, False), "exporter_stdout_bytes": (0, -1, 1048577, True, "128"),
            "exporter_stdout_sha256": ("A" * 64, "a" * 63, None),
            "stdout_eof": (False, 1), "stderr_eof": (False, 1),
            "profile_count": (0, 1, 3, True), "script_count": (0, 1, 3, True),
        }
        variants = []
        for key, values in mutations.items():
            for value in values:
                bad = refused_startup_receipt(case)
                bad["seeded_existing_state"][key] = value
                variants.append(bad)
            missing = refused_startup_receipt(case)
            del missing["seeded_existing_state"][key]
            variants.append(missing)
        for index, receipt in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

    def test_driver_exit_stderr_and_encoding_fail_independently(self):
        fixtures = [(completion_output(), COMPLETION_NAMES, None),
                    (receipt_output("bash-login"), {"bash-login"}, "bash-login")]
        for raw, names, case in fixtures:
            for stderr, code in ((b"diagnostic", 0), (b"", 7)):
                with self.subTest(case=case, stderr=stderr, code=code), self.assertRaises(RuntimeError):
                    receipts.require_driver_pass(raw, stderr, code, names, case)
            with self.assertRaises(UnicodeDecodeError):
                receipts.require_driver_pass(b"\xff" + raw, b"", 0, names, case)

    def test_receipt_missing_duplicate_pass_and_extra_output_reject(self):
        case = "models-filter-sort-limit"
        raw = receipt_output(case)
        pass_line, receipt_line = raw.splitlines(keepends=True)
        variants = [pass_line, receipt_line, raw + pass_line, raw + receipt_line,
                    raw + b"unexpected\n", raw.replace(pass_line, b"PASS: other\n")]
        for index, value in enumerate(variants):
            with self.subTest(index=index), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(value, b"", 0, {case}, case)

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
                receipts.require_driver_pass(receipt_output(case, json.dumps(receipt)), b"", 0, {case}, case)

    def test_duplicate_receipt_keys_cannot_override_failure(self):
        good = json.dumps(valid_receipt("bash-login"), separators=(",", ":"))
        for field, duplicate in (('"exit_code":0', '"exit_code":1,"exit_code":0'),
                                 ('"cleanup_complete":true', '"cleanup_complete":false,"cleanup_complete":true')):
            with self.subTest(duplicate=duplicate), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output("bash-login", good.replace(field, duplicate, 1)),
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
                    receipts.require_driver_pass(receipt_output(case, {**good, key: value}), b"", 0, {case}, case)

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
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

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
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)
        for key, value in (("setup_call_count", 5), ("fresh_shell_count", 2),
                           ("expected_setup_calls", 5), ("expected_fresh_shells", 2),
                           ("setup_stdout_eof", False), ("setup_stderr_eof", False), ("pty_eof", False)):
            receipt = valid_receipt(case)
            receipt[key] = value
            with self.subTest(key=key), self.assertRaises(RuntimeError):
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)

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
                receipts.require_driver_pass(receipt_output(case, receipt), b"", 0, {case}, case)


class ProfileCLIAndCleanupTests(unittest.TestCase):
    def invoke_main(self, args):
        with patch.object(sys, "argv", ["check-wsl-pager.py", *args]), \
                patch.object(pilot.signal, "signal"), patch.object(pilot, "pilot", return_value=0) as run, \
                patch.object(pilot, "resolve_profile", wraps=pilot.resolve_profile) as resolve, \
                patch.object(sys, "stderr", io.StringIO()):
            result = pilot.main()
        resolve.assert_called_once()
        return result, run

    def assert_plan(self, run, selection, payloads):
        run.assert_called_once()
        self.assertEqual(run.call_args.kwargs, {})
        self.assertEqual(len(run.call_args.args), 1)
        plan = run.call_args.args[0]
        self.assertEqual(plan.selection, selection)
        self.assertFalse(plan.cleanup_only)
        self.assertEqual({item.filename: item.path for item in plan.payloads if item.executable}, payloads)
        return plan

    def test_cli_routes_exact_payload_sets(self):
        code, run = self.invoke_main([
            "--profile", "local-state", "--binary", "custom.test", "--process-binary", "process.test",
            "--imageprefs-binary", "imageprefs.test", "--clihelp-binary", "clihelp.test",
            "--autocomplete-binary", "autocomplete.test"])
        self.assertEqual(code, 0)
        plan = self.assert_plan(run, "local-state", {
            name + ".test": Path(name + ".test")
            for name in ("custom", "process", "imageprefs", "clihelp", "autocomplete")})
        self.assertEqual(plan.initial_result["setup_contract"], "supported")
        code, run = self.invoke_main(["--profile", "shell-contracts", "--process-binary", "process.test",
                                     "--cli-binary", "openai"])
        self.assertEqual(code, 0)
        plan = self.assert_plan(run, "shell-contracts", {"process.test": Path("process.test"), "openai": Path("openai")})
        self.assertEqual(plan.initial_result["setup_contract"], "supported")

    def test_cli_routes_refused_contract_with_its_exact_payloads(self):
        code, run = self.invoke_main([
            "--profile", "local-state", "--setup-contract", "refused",
            "--binary", "custom.test", "--process-binary", "process.test",
            "--imageprefs-binary", "imageprefs.test", "--clihelp-binary", "clihelp.test",
            "--autocomplete-binary", "autocomplete.test"])
        self.assertEqual(code, 0)
        plan = self.assert_plan(run, "local-state", {
            name + ".test": Path(name + ".test")
            for name in ("custom", "process", "imageprefs", "clihelp", "autocomplete")})
        self.assertEqual(plan.initial_result["setup_contract"], "refused")
        code, run = self.invoke_main([
            "--profile", "shell-contracts", "--setup-contract", "refused",
            "--process-binary", "process.test", "--cli-binary", "openai",
            "--autocomplete-binary", "autocomplete.test"])
        self.assertEqual(code, 0)
        plan = self.assert_plan(run, "shell-contracts", {
            "process.test": Path("process.test"), "openai": Path("openai"),
            "autocomplete.test": Path("autocomplete.test")})
        self.assertEqual(plan.initial_result["setup_contract"], "refused")

    def test_cli_resolves_once_before_pilot(self):
        plan = resolved_plan("shell-contracts")
        events = []
        def resolve(*_args, **_kwargs):
            events.append("resolve")
            return plan
        def run(actual):
            self.assertIs(actual, plan)
            events.append("pilot")
            return 0
        with patch.object(sys, "argv", ["check-wsl-pager.py", "--profile", "shell-contracts",
                                       "--process-binary", "process.test", "--cli-binary", "openai"]), \
                patch.object(pilot.signal, "signal"), \
                patch.object(pilot, "resolve_profile", side_effect=resolve) as selected, \
                patch.object(pilot, "pilot", side_effect=run) as execute:
            self.assertEqual(pilot.main(), 0)
        selected.assert_called_once()
        execute.assert_called_once_with(plan)
        self.assertEqual(events, ["resolve", "pilot"])

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
            ["--profile", "shell-contracts", "--setup-contract", "refused",
             "--process-binary", "process.test", "--cli-binary", "openai"],
            ["--profile", "shell-contracts", "--setup-contract", "refused",
             "--process-binary", "process.test", "--cli-binary", "openai",
             "--autocomplete-binary", "autocomplete.test", "--clihelp-binary", "clihelp.test"],
            ["--binary", "custom.test", "--setup-contract", "refused"],
            ["--cleanup", "--setup-contract", "refused"],
            ["--profile", "local-state", "--setup-contract", "auto"],
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
            {"setup_contract": "refused"},
            {"profile": "local-state", "setup_contract": "auto"},
            {"profile": "shell-contracts", "setup_contract": "refused", "process_binary": Path("process.test"),
             "extra_payloads": {"openai": Path("openai")}},
            {"profile": "shell-contracts", "setup_contract": "supported", "process_binary": Path("process.test"),
             "extra_payloads": {"openai": Path("openai"), "autocomplete.test": Path("autocomplete.test")}},
        ]
        for kwargs in invalid:
            with self.subTest(kwargs=kwargs), patch.object(pilot, "require_native_windows_process") as native:
                with self.assertRaises(RuntimeError):
                    pilot.resolve_profile(**kwargs)
                native.assert_not_called()

    def test_local_profile_requires_autocomplete_before_native_access(self):
        with patch.object(pilot, "require_native_windows_process") as native:
            with self.assertRaisesRegex(RuntimeError, "exact payload set"):
                pilot.resolve_profile(Path("custom.test"), process_binary=Path("process.test"), profile="local-state",
                                      extra_payloads={"imageprefs.test": Path("imageprefs.test"), "clihelp.test": Path("clihelp.test")})
            native.assert_not_called()

    def test_successor_profiles_reject_command_contract_app_and_image_binaries(self):
        cases = (
            ("local-state", Path("custom.test"), {
                "imageprefs.test": Path("imageprefs.test"), "clihelp.test": Path("clihelp.test"),
                "autocomplete.test": Path("autocomplete.test"),
            }, ["--binary", "custom.test", "--imageprefs-binary", "imageprefs.test",
                "--clihelp-binary", "clihelp.test", "--autocomplete-binary", "autocomplete.test"]),
            ("shell-contracts", None, {"openai": Path("openai")}, ["--cli-binary", "openai"]),
        )
        for profile, binary, extras, flags in cases:
            for name in ("app", "images"):
                with self.subTest(profile=profile, binary=name):
                    args = ["--profile", profile, "--process-binary", "process.test",
                            "--" + name + "-binary", name + ".test", *flags]
                    with patch.object(sys, "argv", ["check-wsl-pager.py", *args]), \
                            patch.object(pilot.signal, "signal"), \
                            patch.object(sys, "stderr", io.StringIO()), patch.object(pilot, "pilot") as run:
                        with self.assertRaises(SystemExit) as caught:
                            pilot.main()
                        self.assertEqual(caught.exception.code, 2)
                        run.assert_not_called()
                    with patch.object(pilot, "require_native_windows_process") as native:
                        with self.assertRaisesRegex(RuntimeError, "Application and image binaries"):
                            pilot.resolve_profile(binary, process_binary=Path("process.test"), profile=profile,
                                                  extra_payloads=extras, **{name + "_binary": Path(name + ".test")})
                        native.assert_not_called()

    def test_cleanup_cli_requires_no_payload_arguments(self):
        for profile in ("local-state", "shell-contracts"):
            for contract in ("supported", "refused"):
                with self.subTest(profile=profile, contract=contract):
                    code, run = self.invoke_main(["--profile", profile, "--cleanup", "--setup-contract", contract])
                    self.assertEqual(code, 0)
                    run.assert_called_once()
                    plan = run.call_args.args[0]
                    self.assertEqual(plan.selection, profile)
                    self.assertTrue(plan.cleanup_only)
                    self.assertEqual(plan.payloads, ())
                    self.assertEqual(plan.phases, ())
                    self.assertEqual(plan.inventory, ())

    def test_cleanup_resolver_does_not_access_payloads_or_inventories(self):
        for profile in ("local-state", "shell-contracts"):
            for contract in ("supported", "refused"):
                with self.subTest(profile=profile, contract=contract), \
                        patch.object(pilot, "require_native_windows_process", side_effect=AssertionError("native access")), \
                        patch.object(pilot, "load_shell_receipts", side_effect=AssertionError("receipt helper loaded")), \
                        patch.object(pilot, "load_local_cases", side_effect=AssertionError("inventory accessed")), \
                        patch.object(pilot, "load_command_cases", side_effect=AssertionError("inventory accessed")), \
                        patch.object(pilot, "require_linux_binary", side_effect=AssertionError("payload accessed")), \
                        patch.object(pilot, "digest", side_effect=AssertionError("asset accessed")), \
                        patch.object(pilot.Path, "is_file", side_effect=AssertionError("asset inspected")), \
                        patch.object(pilot.Path, "is_symlink", side_effect=AssertionError("asset inspected")):
                    plan = pilot.resolve_profile(cleanup_only=True, profile=profile, setup_contract=contract)
                self.assertTrue(plan.cleanup_only)
                self.assertEqual(plan.payloads, ())
                self.assertEqual(plan.phases, ())
                self.assertEqual(plan.inventory, ())

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
                        patch.object(pilot, "load_shell_receipts", side_effect=AssertionError("receipt helper loaded")), \
                        patch.object(pilot, "load_local_cases", side_effect=AssertionError("inventory accessed")) as local, \
                        patch.object(pilot, "load_command_cases", side_effect=AssertionError("inventory accessed")) as prior, \
                        patch.object(pilot, "require_linux_binary", side_effect=AssertionError("payload accessed")) as binary, \
                        patch.object(pilot, "digest", side_effect=AssertionError("asset accessed")) as digest, \
                        patch.object(pilot.Path, "is_file", side_effect=AssertionError("asset inspected")), \
                        patch.object(pilot.Path, "is_symlink", side_effect=AssertionError("asset inspected")), \
                        patch.object(pilot, "clean_distro") as clean, \
                        patch.dict(os.environ, {"SYSTEMROOT": str(path / "windows")}):
                    self.assertEqual(pilot.pilot(pilot.resolve_profile(cleanup_only=True, profile=profile)), 0)
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
