"""Validate captured results from the fixed WSL shell profile."""

import base64
from collections import Counter
import hashlib
import json
import re


def require_driver_pass(stdout, stderr, code, names, receipt_case=None):
    text = stdout.decode("utf-8", errors="strict")
    lines = text.splitlines()
    passed = Counter(line[6:] for line in lines if line.startswith("PASS: "))
    if code != 0 or stderr or passed != Counter(names):
        raise RuntimeError("Missing, duplicated, or failed WSL shell scene")
    if receipt_case is None:
        if len(lines) != len(names) + 1 or not lines[0].startswith("GNU bash, version "):
            raise RuntimeError("Unexpected Bash completion output")
        return {"cases": sorted(names)}
    receipts = [line[9:] for line in lines if line.startswith("RECEIPT: ")]
    if len(receipts) != 1 or len(lines) != len(names) + 1:
        raise RuntimeError("Missing or duplicated WSL shell receipt")
    def unique_object(pairs):
        value = dict(pairs)
        if len(value) != len(pairs):
            raise RuntimeError("Duplicated WSL shell receipt key")
        return value
    receipt = json.loads(receipts[0], object_pairs_hook=unique_object)
    if (not isinstance(receipt, dict) or receipt.get("case") != receipt_case
            or type(receipt.get("exit_code")) is not int or receipt["exit_code"] != 0
            or receipt.get("cleanup_complete") is not True or receipt.get("failures") != []):
        raise RuntimeError("Invalid WSL shell receipt")
    if receipt_case == "models-filter-sort-limit":
        expected_counts = {"cli_call_count": 1, "cli_exit_code": 0, "request_count": 1,
                           "accepted_request_count": 1, "stdout_bytes": 121, "stderr_bytes": 0,
                           "response_bytes": 5834}
        required_true = ("stdout_eof", "stderr_eof", "openai_settings_absent", "fixture_thread_joined")
        expected_output = ('{"id":"demo-text-2026-10-01-001","object":"model","created":1700000000,'
                           '"owned_by":"system","shutdown_date":"2030-01-01"}\n')
        if (receipt.get("stdout_utf8") != expected_output
                or receipt.get("stdout_sha256") != hashlib.sha256(expected_output.encode()).hexdigest()
                or receipt.get("response_sha256") != "65a35e98b76235a982e331fe094c5d044e3e6380c7bf2de51655459ff77f6d47"):
            raise RuntimeError("Models receipt lost the complete selected record")
    else:
        refused = receipt_case in {"bash-login-refused", "bash-nonlogin-refused"}
        if receipt_case not in {"bash-login", "bash-nonlogin", "bash-login-refused", "bash-nonlogin-refused"}:
            raise RuntimeError("Unknown startup receipt case")
        call_count = 7 if refused else 6
        expected_counts = {"setup_call_count": call_count, "fresh_shell_count": 3,
                           "expected_setup_calls": call_count, "expected_fresh_shells": 3}
        required_true = ("setup_stdout_eof", "setup_stderr_eof", "pty_eof", "system_sources_unchanged")
        if refused:
            required_true += ("original_profiles_unchanged", "seeded_profiles_unchanged", "recovery_evidence_unchanged",
                              "absent_profiles_preserved", "opt_out_created", "generated_scripts_removed",
                              "unselected_profile_unchanged")
            seeded = receipt.get("seeded_existing_state")
            if (receipt.get("setup_contract") != "refused"
                    or receipt.get("installation_scope") != "Seeded existing state tests activation and cleanup, not installation."
                    or receipt.get("automatic_setup_scope") != (
                        "Separate account-home admission is recorded in first_use_admission."
                        if receipt_case == "bash-nonlogin-refused" else
                        "Not exercised: private HOME differs from the account home.")
                    or not isinstance(seeded, dict) or seeded.get("contract") != "seeded-existing-state"
                    or seeded.get("stdout_eof") is not True or seeded.get("stderr_eof") is not True
                    or any(type(seeded.get(key)) is not int or seeded[key] != value for key, value in
                           {"exporter_exit_code": 0, "exporter_stderr_bytes": 0, "profile_count": 2, "script_count": 2}.items())
                    or type(seeded.get("exporter_stdout_bytes")) is not int
                    or not 0 < seeded["exporter_stdout_bytes"] <= 1048576
                    or not re.fullmatch(r"[0-9a-f]{64}", str(seeded.get("exporter_stdout_sha256", "")))):
                raise RuntimeError("Refused startup receipt lost its seeded-state evidence boundary")
            if receipt_case == "bash-nonlogin-refused":
                require_first_use_receipt(receipt.get("first_use_admission"))
        sources = receipt.get("system_sources")
        if (not isinstance(sources, list) or len(sources) != 2
                or any(not isinstance(item, dict) for item in sources)
                or [item.get("path") for item in sources] != ["/etc/profile", "/etc/bash.bashrc"]
                or json.dumps(receipt.get("system_sources_after"), sort_keys=True) != json.dumps(sources, sort_keys=True)):
            raise RuntimeError("Startup receipt lost its system source binding")
        identity_fields = {"device", "inode", "mode", "uid", "gid", "size", "mtime_ns", "ctime_ns"}
        for item in sources:
            if type(item.get("present")) is not bool:
                raise RuntimeError("Startup source presence is invalid")
            if not item["present"]:
                if set(item) != {"path", "present"}:
                    raise RuntimeError("Absent startup source has unexpected evidence")
                continue
            identity = item.get("identity")
            if (not isinstance(identity, dict) or set(identity) != identity_fields
                    or any(type(value) is not int for value in identity.values())
                    or identity["size"] != item.get("bytes") or identity["mode"] & 0o170000 != 0o100000):
                raise RuntimeError("Startup source identity is invalid")
            receipt_bytes({"source_base64": item.get("content_base64"), "source_bytes": item.get("bytes"),
                           "source_sha256": item.get("sha256")}, "source", 65536)
        calls, shells = receipt.get("setup_calls"), receipt.get("fresh_shells")
        actions = (["install-refused-text", "install-refused-json", "install-refused-again", "seeded-install-noop",
                    "seeded-remove-refused-text", "seeded-remove-refused-json", "absent-profile-remove"] if refused else
                   ["install", "install-again", "remove", "remove-again", "reenable", "remove-final"])
        checkpoints = (["fresh-original", "fresh-seeded-existing", "fresh-after-refused-remove"] if refused else
                       ["fresh-installed", "fresh-removed", "fresh-reenabled"])
        if (not isinstance(calls, list) or len(calls) != call_count or not isinstance(shells, list) or len(shells) != 3
                or any(not isinstance(item, dict) for item in calls + shells)
                or [item.get("action") for item in calls] != actions
                or [item.get("checkpoint") for item in shells] != checkpoints):
            raise RuntimeError("Startup receipt lost a lifecycle action")
        for item in calls + shells:
            expected_exit = int(refused and "refused" in item.get("action", ""))
            if type(item.get("exit_code")) is not int or item["exit_code"] != expected_exit:
                raise RuntimeError("Startup receipt contains a failed command")
            if refused and item in calls and (type(item.get("expected_exit_code")) is not int
                                              or item["expected_exit_code"] != expected_exit):
                raise RuntimeError("Refused startup receipt changed an expected exit status")
        if any(item.get("stdout_eof") is not True or item.get("stderr_eof") is not True for item in calls):
            raise RuntimeError("Startup receipt contains incomplete setup output")
        if any(item.get("pty_eof") is not True or item.get("forced_cleanup") is not False for item in shells):
            raise RuntimeError("Startup receipt contains incomplete fresh shells")
        for item in calls:
            for stream in ("stdout", "stderr"):
                raw = receipt_bytes(item, stream, 1048576)
                if stream == "stdout" and raw:
                    raise RuntimeError("Startup setup wrote unexpected stdout")
            if refused:
                is_remove = "remove" in item["action"]
                error_format = "json" if item["action"].endswith("-json") else "text"
                if item.get("format_error") != error_format:
                    raise RuntimeError("Refused startup receipt lost its output format")
                if item["expected_exit_code"]:
                    operation = "removal" if is_remove else "setup"
                    message = ("Could not finish Tab shortcut " + operation
                               + ": this environment does not support the required atomic rename."
                               + (" Existing shortcuts may remain active." if is_remove else "")
                               + " Type openai images generate and press Enter to use the picker."
                               + " Some startup files or preferences may already have changed."
                               + " Keep any .openai-picker-recovery-* directories beside the startup file; inspect their original files before manual recovery."
                               + " Persistent setup needs a filesystem with atomic rename support.")
                    if error_format == "json":
                        if json.loads(raw.decode("utf-8"), object_pairs_hook=unique_object) != {"message": message}:
                            raise RuntimeError("Refused startup receipt lost its JSON diagnostic")
                    elif raw != (message + "\n").encode():
                        raise RuntimeError("Refused startup receipt lost its text diagnostic")
                else:
                    message = ("Tab shortcut setup removed. Open a new terminal to finish.\n" if is_remove else
                               "Tab setup saved for future terminals. Existing custom bindings are preserved.\n")
                    if raw != message.encode():
                        raise RuntimeError("Seeded-state cleanup receipt changed")
        if refused and (any(type(item.get("expected_active")) is not bool for item in shells)
                        or [item["expected_active"] for item in shells] != [False, True, True]):
            raise RuntimeError("Refused startup receipt lost its fresh-shell states")
        for item in shells:
            raw = receipt_bytes(item, "pty", 1048576)
            if b"__PROFILE_READY__> " not in raw:
                raise RuntimeError("Startup receipt lost its prompt evidence")
    if any(type(receipt.get(key)) is not int or receipt[key] != value for key, value in expected_counts.items()):
        raise RuntimeError("WSL shell receipt has unexpected command or request counts")
    if any(receipt.get(key) is not True for key in required_true):
        raise RuntimeError("WSL shell receipt lacks complete output or cleanup")
    return receipt


def require_first_use_receipt(receipt):
    if (not isinstance(receipt, dict)
            or receipt.get("account") != {"name": "cli-compat", "uid": 2000, "home": "/home/cli-compat"}
            or type(receipt.get("call_count")) is not int or receipt["call_count"] != 4
            or any(receipt.get(key) is not True for key in ("cleanup_complete", "profiles_unchanged", "opt_out_preserved"))
            or receipt.get("removed_environment_variables") != ["CI"]):
        raise RuntimeError("First-use receipt lost its admission or cleanup contract")
    before = receipt.get("profiles_before")
    expected_paths = ["/home/cli-compat/" + name for name in
                      (".bashrc", ".bash_profile", ".bash_login", ".profile", ".bash_logout")]
    if (not isinstance(before, list) or len(before) != 5 or any(not isinstance(item, dict) for item in before)
            or [item.get("path") for item in before] != expected_paths
            or receipt.get("profiles_after") != before):
        raise RuntimeError("First-use receipt lost unchanged account profiles")
    identity_fields = {"device", "inode", "mode", "uid", "gid", "size", "mtime_ns", "ctime_ns"}
    for source in before:
        if source.get("present") is False and set(source) == {"path", "present"}:
            continue
        identity = source.get("identity")
        if (source.get("present") is not True or not isinstance(identity, dict)
                or set(identity) != identity_fields or any(type(value) is not int for value in identity.values())
                or identity["size"] != source.get("bytes") or identity["mode"] & 0o170000 != 0o100000):
            raise RuntimeError("First-use receipt lost profile identity")
        receipt_bytes({"profile_base64": source.get("content_base64"), "profile_bytes": source.get("bytes"),
                       "profile_sha256": source.get("sha256")}, "profile", 65536)
    calls = receipt.get("calls")
    expected = (("ci-ineligible", False), ("eligible-first", True), ("eligible-repeat", True), ("opted-out", False))
    if not isinstance(calls, list) or len(calls) != len(expected):
        raise RuntimeError("First-use receipt lost an invocation")
    baseline = None
    for item, (name, attempted) in zip(calls, expected):
        if (not isinstance(item, dict) or item.get("case") != name
                or item.get("attempted_setup") is not attempted
                or type(item.get("exit_code")) is not int or item["exit_code"] != 0
                or item.get("pty_eof") is not True):
            raise RuntimeError("First-use receipt changed its cases, status or EOF")
        raw = receipt_bytes(item, "pty", 1048576)
        if baseline is None:
            baseline = raw
        if (raw != baseline or b"NAME:\r\n   openai" not in raw or b"SYNOPSIS:\r\n" not in raw
                or not raw.endswith(b"\r\nFIRST_USE_STATUS:0\r\n")):
            raise RuntimeError("First-use receipt changed ordinary help output")
        stages = item.get("staging")
        if not isinstance(stages, dict) or set(stages) != {"created", "deleted", "sequence"}:
            raise RuntimeError("First-use receipt lost staging evidence")
        for values in (stages["created"], stages["deleted"]):
            if (not isinstance(values, list) or len(values) > 256
                    or any(not isinstance(value, str) or not re.fullmatch(r"\.openai-picker-[A-Z2-7]{26}", value)
                           for value in values)):
                raise RuntimeError("First-use receipt has invalid staging evidence")
        sequence = stages["sequence"]
        if not isinstance(sequence, list) or len(sequence) > 256:
            raise RuntimeError("First-use receipt lost ordered staging events")
        active, seen, created, deleted = set(), set(), [], []
        for event in sequence:
            if (not isinstance(event, dict) or set(event) != {"name", "mask"}
                    or event["name"] not in stages["created"]):
                raise RuntimeError("First-use receipt has an unknown staging event")
            name = event["name"]
            if event["mask"] == 0x100 and name not in seen:
                seen.add(name)
                active.add(name)
                created.append(name)
            elif event["mask"] == 0x200 and name in active:
                active.remove(name)
                deleted.append(name)
            else:
                raise RuntimeError("First-use receipt duplicated or reordered a staging event")
        if (active or created != stages["created"] or deleted != stages["deleted"]
                or bool(created) != attempted):
            raise RuntimeError("First-use receipt lacks the required positive and negative admission controls")


def receipt_bytes(item, field, limit):
    encoded = item.get(field + "_base64")
    if not isinstance(encoded, str) or len(encoded) > 4 * ((limit + 2) // 3):
        raise RuntimeError("Missing or excessive startup byte evidence")
    try:
        raw = base64.b64decode(encoded, validate=True)
    except (ValueError, TypeError) as error:
        raise RuntimeError("Invalid startup byte evidence") from error
    if (len(raw) > limit or type(item.get(field + "_bytes")) is not int
            or item[field + "_bytes"] != len(raw)):
        raise RuntimeError("Startup byte evidence has a mismatched length")
    if field != "stdout" and item.get(field + "_sha256") != hashlib.sha256(raw).hexdigest():
        raise RuntimeError("Startup byte evidence has a mismatched digest")
    return raw

