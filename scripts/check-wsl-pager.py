#!/usr/bin/env python3
"""Run fixed compatibility checks in one disposable Ubuntu WSL 1 distro.

This is a process check. It does not test Windows Terminal or WSL 2.
The calling workflow cross-compiles the test binary before importing Ubuntu.
"""

import argparse
import copy
from dataclasses import dataclass, field
from functools import partial
import importlib.util
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import shutil
import signal
import struct
import subprocess
import sys
import threading
import time
import urllib.error
import urllib.request


# Use the kernel.org Ubuntu mirror: the Canonical origin repeatedly stalls in CI.
IMAGE_URL = "https://mirrors.edge.kernel.org/ubuntu-releases/24.04.5/ubuntu-24.04.5-wsl-amd64.wsl"
# Canonical SHA256SUMS and Microsoft/WSL f72a0e04 DistributionInfo.json agree.
IMAGE_SHA256 = "bb415d824822c4b878125729af451a5d18fb13d1cf5cbed9a7393ad64ac6039e"
IMAGE_LIMIT = 512 * 1024 * 1024
OUTPUT_LIMIT = 8 * 1024 * 1024
ROOT = "TestSocketPairPagerFailureKeepsReusedDescriptorOpen"
TESTS = {ROOT, ROOT + "/LookPath", ROOT + "/ForkExec"}

# Public input, download, output, and image-saving contracts invoke main() in subprocesses.
PROCESS_CASES = {
    "TestMainShellExplicitTextInput": [
        "plain/untrusted=false", "plain/untrusted=true", "JSON_stays_text/untrusted=false",
        "JSON_stays_text/untrusted=true", "YAML_stays_text/untrusted=false", "YAML_stays_text/untrusted=true",
        "empty/untrusted=false", "empty/untrusted=true", "unicode_and_control/untrusted=false",
        "unicode_and_control/untrusted=true", "base64/untrusted=false", "base64/untrusted=true",
        "non_UTF8_auto_base64/untrusted=false", "non_UTF8_auto_base64/untrusted=true",
        "stdin_content_cannot_expand_files/untrusted=false",
        "stdin_content_cannot_expand_files/untrusted=true",
    ],
    "TestMainShellWholeRequestAndRepeatedFlags": [
        "JSON_override_and_unknown_null", "YAML", "last_scalar_releases_stdin", "last_scalar_selects_stdin",
        "nested_field_selects_stdin", "nested_replacement_releases_stdin",
        "empty_nullable_replacement_releases_stdin", "null_nullable_replacement_releases_stdin",
        "untrusted_reference_remains_literal",
    ],
    "TestMainShellStdinConflictBeforeRead": [
        "responses_create_--model_model_synthetic_--input_@-_--instructions_@-",
        "audio_transcriptions_create_--model_whisper-1_--file_-_--prompt_@-",
        "images_edit_--model_dall-e-2_--prompt_synthetic_--image_-_--image_-",
        "responses_create_--model_model_synthetic_--input_[{\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"@-\"},{\"type\":\"input_text\",\"text\":\"@data://-\"}]}]",
        "chat_completions_create_--model_model_synthetic_--message_{\"role\":\"user\",\"content\":\"@-\"}_--message_{\"role\":\"user\",\"content\":\"@file://-\"}",
    ],
    "TestMainShellStreamingInputDispatch": [
        "JSON_true", "YAML_true", "explicit_false", "explicit_null", "last_stream_false", "last_stream_null",
        "last_stream_true", "body_false", "body_null", "omitted",
    ],
    "TestMainShellMachineStreamDeliversBeforeNextEvent": [
        "body/jsonl", "body/json", "body/pretty", "body/raw", "body/yaml", "flag/jsonl", "flag/json",
        "flag/pretty", "flag/raw", "flag/yaml",
    ],
    "TestMainShellSaveReceiptWaitsForBodyCompletion": [],
    "TestMainShellClosedStdoutStopsDownload": [],
    "TestMainShellClosedStdoutPreservesStreamFailure": [],
    "TestMainFilesWorkflowUploadPaths": [
        "spaces", "options_first", "apostrophe", "literal_at", "literal_dash", "literal_double_dash", "empty",
        "binary", "upload_flag", "create_flag", "upload_assigned_flag", "upload_repeated_flag",
        "create_repeated_flag",
    ],
    "TestMainFilesWorkflowUploadValidation": [
        "missing_purpose", "missing_path", "missing_local_file", "extra_positional",
        "positional_and_file_flag", "file_flag_and_positional", "create_keeps_flag_syntax",
    ],
    "TestMainFilesWorkflowExplicitBinaryStdin": [
        "empty", "binary", "JSON_stays_file_bytes",
    ],
    "TestMainFilesWorkflowMachineOutput": [
        "piped_default", "explicit_auto", "explicit_text", "json", "jsonl", "raw", "extract_ID", "raw_ID",
        "raw_timestamp",
    ],
    "TestMainFilesWorkflowDownloadBytes": [
        "text", "binary",
    ],
    "TestMainDispatchOutputQuietPreservesSelectedPayloads": [
        "models_retrieve_model_synthetic", "--format_json_models_retrieve_model_synthetic",
        "--format_jsonl_files_list", "--format_yaml_files_list", "--format_raw_files_list",
        "--format_pretty_models_retrieve_model_synthetic",
        "--transform_extra.note_--raw-output_models_retrieve_model_synthetic",
        "files_content_file_synthetic_--output_-", "containers_delete_cntr_synthetic",
    ],
    "TestMainDispatchOutputVerboseKeepsDataAndProtectsMachineErrors": [
        "human_success", "machine_stdout_text_stderr", "machine_success", "machine_failure",
        "explicit_machine_error", "human_failure", "quiet_takes_precedence",
    ],
    "TestMainDispatchOutputVerboseBinaryKeepsBytesAndLabelsFormatOption": [],
    "TestMainDispatchOutputQuietKeepsFailurePayloadsAndSuppressesFallbackNotice": [
        "json", "jsonl", "raw", "explore",
    ],
    "TestMainImageGenerationPartialFailureKeepsSavedImage": [],
    "TestMainImageGenerationStreamSavesOnlyFinalAndClosesResponse": [
        "--stream=true", "--partial-images_2", "--stream=true_--max-items_-1",
        "--partial-images_2_--max-items_-1", "--stream=true_--max-items_-2",
    ],
    "TestMainImageGenerationClosedStdoutKeepsSavedImage": [
        "partial-failure=false/format=text", "partial-failure=true/format=text",
        "partial-failure=false/format=json", "partial-failure=true/format=json",
    ],
    "TestMainImageEditStreamSavesFinalAndCloses": ["explicit", "partials", "stdin", "YAML"],
    "TestMainImageUploadBatchCollisionAndPartialFailure": [
        "edit_partial=false", "edit_partial=true", "create-variation_partial=false", "create-variation_partial=true",
    ],
    "TestMainImageEditInterruptClosesStreamWithoutSavingPartial": [],
}
PROCESS_TESTS = {root for root in PROCESS_CASES} | {
    root + "/" + child for root, children in PROCESS_CASES.items() for child in children
}
PROCESS_SELECTOR = "^(" + "|".join(PROCESS_CASES) + ")$"
COMMAND_CASES_PATH = Path(__file__).with_name("wsl-command-cases.json")
LOCAL_CASES_PATH = Path(__file__).with_name("wsl-local-state-cases.json")
INDEPENDENT_PROCESS_ROOTS = {
    "TestMainLocalUtilitiesIgnoreRemoteConfiguration",
    "TestMainLocalUtilitiesRootFlagPlacement",
    "TestMainLocalUtilitiesLegacyTokenizers",
    "TestMainLocalUtilitiesNamesDoNotBypassRemoteValidation",
    "TestMainLocalUtilitiesPreserveRemoteTokenCounter",
    "TestMainLocalUtilitiesErrorsRemainLocal",
    "TestMainTokenizerEditorNeverCapturesRedirectedInput",
    "TestMainTokenizerEditorPlainFallbackEnvironments",
    "TestMainTokenizerEditorRejectsUnsupportedModes",
    "TestMainTokenizerEditorLeavesScriptResultsUnchanged",
    "TestMainAdminSetupOfflineRoutes",
    "TestMainAdminSetupIsReadOnly",
    "TestMainAdminSetupPreservesOrdinarySetup",
    "TestMainAdminSetupRejectsExtraArguments",
    "TestMainImageModelsRequestDeadlineCancelsHTTP",
    "TestMainImageModelsInterruptPreservesCompletedResults",
    "TestMainWelcomeRedirectedOutputMatchesHelp",
    "TestMainWelcomeExplicitArgumentsStayPlain",
    "TestMainWelcomeBareInvocationStaysOffline",
    "TestMainCommandSubgroupsBareInvocation",
    "TestMainDispatchOutputManpagesDeepSubgroups",
    "TestMainPickerShellSetupRefusesUnsafeConfigurationAncestry",
    "TestMainPickerShellSetupReadOnlyModesDoNotWrite",
}
SUPPORTED_AUTOCOMPLETE_ROOTS = {
    "TestPickerRenameNoReplace", "TestPickerInstallRoundTrip", "TestPickerRemoveRestoresAbsentProfile",
}
NATIVE_CUSTOM_ROOT = "TestImagePickerFirstRunNativeSetupContract"
NATIVE_PROCESS_ROOT = "TestMainPickerShellSetupNativeContract"
PROFILE_PHASES = {
    # Direct installer checks retain native errors before public commands redact them.
    "local-state": {"imageprefs": 70, "clihelp": 70, "custom": 70,
                    "process": 170, "autocomplete": 70, "final": 20},
    "shell-contracts": {"invocation": 170, "completion": 75, "startup-login": 130,
                        "startup-nonlogin": 130, "models": 40, "final": 20},
}
NATIVE_CASES = {"TestMainNativeShell": [
    "bash", "bash/installed_help_uses_command_name",
    "bash/copy_local_help_with_implicit_lookup_enabled",
    "bash/copy_typo_suggestion_with_another_openai_on_PATH",
]}
NATIVE_SELECTOR = ("^TestMainNativeShell$/^bash$/^(installed_help_uses_command_name|"
                   "copy_local_help_with_implicit_lookup_enabled|"
                   "copy_typo_suggestion_with_another_openai_on_PATH)$")
COMPLETION_CASES = {
    "single quoted assigned file", "double quoted assigned file", "double quoted apostrophe",
    "escaped assigned file", "separate quoted file", "unquoted assigned control",
    "UTF-8 midline file with later flag",
    "escaped colon command", "quoted colon command", "unquoted colon command",
    "literal colon flag value", "separate colon command words",
}
SHELL_ASSETS = ("check-bash-completion.py", "image_picker_harness.py",
                "check-wsl-shell-startup.py", "check-wsl-models.py")
PAYLOAD_ROOT = "/home/cli-compat/payload/"
WELCOME_DYNAMIC_ROOT = "TestMainWelcomeBareInvocationStaysOffline/"
WELCOME_DYNAMIC_NAME = WELCOME_DYNAMIC_ROOT + "<loopback>"

STOP = 0


def interrupted(number, _frame):
    global STOP
    # Do not interrupt process creation before we retain its process handle.
    STOP = STOP or number


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def decode_metadata(data):
    """WSL inventory can be UTF-16LE, including output without a BOM."""
    if data.startswith((b"\xff\xfe", b"\xfe\xff")):
        text = data.decode("utf-16")
    elif b"\0" in data:
        text = data.decode("utf-16-le")
    else:
        text = data.decode("utf-8-sig")
    if any(ord(char) < 32 and char not in "\r\n\t" for char in text):
        raise RuntimeError("Invalid WSL metadata controls")
    return text


def distribution_names(data):
    names = [line.strip() for line in decode_metadata(data).splitlines() if line.strip()]
    if len(names) != len(set(names)):
        raise RuntimeError("Duplicate WSL distribution names")
    return names


def require_version_one(data, name):
    rows = re.findall(r"^\s*\*?\s*" + re.escape(name) + r"\s+\S+\s+(\d+)\s*$",
                      decode_metadata(data), re.MULTILINE)
    if rows != ["1"]:
        raise RuntimeError("The owned distribution is not verified as WSL 1")


def require_feature_enabled(data, stderr):
    if stderr:
        raise RuntimeError("The Windows feature query wrote stderr")
    try:
        text = decode_metadata(data)
    except UnicodeError as error:
        raise RuntimeError("Invalid Windows feature encoding") from error
    names = re.findall(r"^[ \t]*Feature Name[ \t]*:[ \t]*([^\r\n]*)\r?$", text, re.MULTILINE)
    states = re.findall(r"^[ \t]*State[ \t]*:[ \t]*([^\r\n]*)\r?$", text, re.MULTILINE)
    if ([value.strip() for value in names] != ["Microsoft-Windows-Subsystem-Linux"] or
            [value.strip() for value in states] != ["Enabled"]):
        raise RuntimeError("WSL 1 is not verified as already enabled")


def require_native_windows_process():
    identity = {"platform": sys.platform, "machine": platform.machine(),
                "pointer_bits": struct.calcsize("P") * 8, "pid": os.getpid(),
                "python_executable": sys.executable, "python_version": sys.version}
    if (identity["platform"] != "win32" or identity["pointer_bits"] != 64 or
            identity["machine"].lower() not in ("amd64", "x86_64")):
        raise RuntimeError("This pilot requires a 64-bit Python process on Windows AMD64")
    return identity


def require_test_pass(stdout, stderr, code, tests=TESTS, label="pager", dynamic_loopback=False):
    text = stdout.decode("utf-8", errors="strict")
    stderr.decode("utf-8", errors="strict")
    if code != 0 or stderr:
        raise RuntimeError(f"The WSL {label} test failed or wrote stderr")
    starts = Counter(re.findall(r"^=== RUN\s+(\S+)\s*$", text, re.MULTILINE))
    passes = Counter(re.findall(r"^\s*--- PASS: (\S+) \([^\n]*\)\s*$", text, re.MULTILINE))
    observed = set(starts)
    if dynamic_loopback:
        # Match the one source-generated port. Never normalize mismatched run/pass names.
        if starts != passes or WELCOME_DYNAMIC_NAME in starts:
            raise RuntimeError("Mismatched dynamic test run/pass names")
        matches = [name for name in starts if name.startswith(WELCOME_DYNAMIC_ROOT + "http:")]
        pattern = re.escape(WELCOME_DYNAMIC_ROOT) + r"http://(?:127\.0\.0\.1|\[::1\]):([1-9][0-9]{0,4})"
        matched = re.fullmatch(pattern, matches[0]) if len(matches) == 1 else None
        if matched is None or not 1 <= int(matched.group(1)) <= 65535:
            raise RuntimeError("Invalid dynamic loopback test name")
        actual = matches[0]
        for counts in (starts, passes):
            counts[WELCOME_DYNAMIC_NAME] = counts.pop(actual)
    if (starts != Counter(tests) or passes != Counter(tests) or
            re.search(r"--- (?:FAIL|SKIP):|panic:|fatal error:|no tests to run", text) or
            len(re.findall(r"^PASS\s*$", text, re.MULTILINE)) != 1):
        raise RuntimeError(f"Missing, skipped, duplicated, or failed {label} test execution")
    return observed


def require_linux_binary(path):
    if path.is_symlink() or not path.is_file():
        raise RuntimeError("Expected a regular test binary")
    with path.open("rb") as source:
        header = source.read(20)
    if header[:6] != b"\x7fELF\x02\x01" or header[18:20] != b"\x3e\x00":
        raise RuntimeError("Expected a Linux AMD64 ELF test binary")
    return digest(path)


class Capture:
    def __init__(self, stream):
        self.stream, self.data = stream, bytearray()
        self.overflow = False
        self.error = None
        self.done = threading.Event()
        self.thread = threading.Thread(target=self.read, daemon=True)
        self.thread.start()

    def read(self):
        try:
            while chunk := self.stream.read(65536):
                available = max(0, OUTPUT_LIMIT - len(self.data))
                self.data.extend(chunk[:available])
                self.overflow |= len(chunk) > available
                # Continue draining excess bytes during cleanup, without storing them.
        except Exception as error:
            self.error = type(error).__name__
        finally:
            self.done.set()


class Commands:
    def __init__(self, directory, deadline, prefix=""):
        self.directory, self.deadline, self.prefix = directory, deadline, prefix
        self.records, self.captures, self.processes = [], [], []

    def run(self, label, argv, seconds, *, cleanup=False, expected=0):
        if time.monotonic() >= self.deadline or (STOP and not cleanup):
            raise RuntimeError("WSL pilot stopped or reached its deadline")
        allowed = {"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATH", "TEMP", "TMP"}
        env = {key: value for key, value in os.environ.items() if key.upper() in allowed}
        started = time.monotonic()
        process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, env=env, bufsize=0)
        self.processes.append(process)
        record = {"label": label, "argv": [str(a) for a in argv], "pid": process.pid}
        self.records.append(record)
        captures = []
        end = min(self.deadline, started + seconds)
        reason = None
        stop_requested = False
        try:
            for stream in (process.stdout, process.stderr):
                capture = Capture(stream)
                captures.append(capture)
                self.captures.append(capture)
            while process.poll() is None:
                if time.monotonic() >= end:
                    reason = "timeout"
                elif STOP and not cleanup:
                    reason = "interrupted"
                elif any(c.overflow or c.error for c in captures):
                    reason = "output capture failed"
                if reason:
                    # Windows Popen owns a process HANDLE. Never taskkill a numeric PID.
                    process.kill()
                    stop_requested = True
                    break
                time.sleep(0.02)
            record["exit_code"] = process.wait(timeout=5)
            if time.monotonic() >= end:
                reason = reason or "late completion"
        finally:
            if process.poll() is None:
                process.kill()
                stop_requested = True
                process.wait(timeout=5)
            record["exit_code"] = process.returncode
            for capture in captures:
                capture.thread.join(timeout=max(0, min(1, end - time.monotonic())))
            if time.monotonic() >= end:
                reason = reason or "late completion"
            record["forced_stop"] = reason if stop_requested else None
            record["stop_requested"] = stop_requested
            record["elapsed_seconds"] = round(time.monotonic() - started, 3)
            record["stream_eof"] = [capture.done.is_set() and not capture.error for capture in captures]
            record["overflow"] = any(c.overflow for c in captures)
            index = len(self.records)
            for suffix, capture in zip(("stdout", "stderr"), captures):
                path = self.directory / f"{self.prefix}{index:02d}-{label}.{suffix}"
                snapshot = bytes(capture.data)
                path.write_bytes(snapshot)
                record[suffix] = {"file": path.name, "sha256": digest(path), "bytes": len(snapshot)}
            if time.monotonic() >= end:
                reason = reason or "late completion"
            record["failure_reason"] = reason
        if reason or not all(record["stream_eof"]) or record["overflow"]:
            raise RuntimeError(f"{label}: incomplete process or output capture")
        if expected is not None and record["exit_code"] != expected:
            raise RuntimeError(f"{label}: unexpected exit {record['exit_code']}")
        return bytes(captures[0].data), bytes(captures[1].data), record["exit_code"]

    def finish(self):
        for process in self.processes:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=max(0, min(5, self.deadline - time.monotonic())))
        for capture in self.captures:
            capture.thread.join(timeout=max(0, min(1, self.deadline - time.monotonic())))
            if not capture.done.is_set():
                raise RuntimeError("An owned output stream did not close")
            capture.stream.close()


class HTTPSOnly(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, newurl):
        if not newurl.startswith("https://mirrors.edge.kernel.org/ubuntu-releases/"):
            raise RuntimeError("Unexpected Ubuntu image redirect")
        return super().redirect_request(request, fp, code, message, headers, newurl)


def open_image(opener, deadline, attempts):
    # Retry only startup timeouts. No response or output file exists yet.
    for index in range(2):
        started = time.monotonic()
        remaining = deadline - started
        if STOP or remaining <= 0:
            raise RuntimeError("Ubuntu download stopped or timed out")
        attempt = {"number": index + 1, "outcome": "opening"}
        attempts.append(attempt)
        try:
            response = opener.open(IMAGE_URL, timeout=min(10, remaining))
        except Exception as error:
            reason = error.reason if isinstance(error, urllib.error.URLError) else error
            attempt.update(outcome="failed", error_type=type(error).__name__,
                           reason_type=type(reason).__name__,
                           elapsed_seconds=round(time.monotonic() - started, 3))
            if index or isinstance(error, urllib.error.HTTPError) or not isinstance(reason, TimeoutError):
                raise
            if STOP or deadline - time.monotonic() < 11:
                raise
            time.sleep(1)
            if STOP or deadline - time.monotonic() < 10:
                raise
            continue
        attempt.update(outcome="opened", elapsed_seconds=round(time.monotonic() - started, 3))
        if STOP or time.monotonic() >= deadline:
            attempt["outcome"] = "rejected_late_or_stopped"
            response.close()
            raise RuntimeError("Ubuntu download stopped or timed out")
        return response
    raise RuntimeError("Ubuntu download failed to open within retry policy")


def download_image(path, deadline, attempts=None, *, staged_image=None):
    # A socket read can take ten seconds. Reserve that time inside the 480-second cap.
    end = min(deadline, time.monotonic() + 470)
    hasher, size = hashlib.sha256(), 0
    if staged_image is None:
        opener = urllib.request.build_opener(HTTPSOnly())
        source = open_image(opener, end, attempts if attempts is not None else [])
    else:
        # Artifacts are transport only: verify the bytes again before WSL import.
        if staged_image.is_symlink() or not staged_image.is_file():
            raise RuntimeError("Staged Ubuntu image must be a regular file")
        source = staged_image.open("rb")
    with source as response, path.open("xb") as output:
        declared_size = (int(response.headers.get("Content-Length", "0")) if staged_image is None
                         else os.fstat(response.fileno()).st_size)
        if declared_size > IMAGE_LIMIT:
            raise RuntimeError("Ubuntu image exceeds its download limit")
        while True:
            if STOP or time.monotonic() >= end:
                raise RuntimeError("Ubuntu download stopped or timed out")
            chunk = response.read1(65536)
            if STOP or time.monotonic() >= end:
                raise RuntimeError("Ubuntu download stopped or timed out")
            if not chunk:
                break
            size += len(chunk)
            if size > IMAGE_LIMIT:
                raise RuntimeError("Ubuntu image exceeds its download limit")
            output.write(chunk)
            hasher.update(chunk)
    if STOP or time.monotonic() >= end:
        raise RuntimeError("Ubuntu download stopped or timed out")
    if hasher.hexdigest() != IMAGE_SHA256:
        raise RuntimeError("Ubuntu image checksum mismatch")
    return size


def owned_paths():
    values = [os.environ.get(key, "") for key in ("GITHUB_RUN_ID", "GITHUB_RUN_ATTEMPT")]
    if not all(re.fullmatch(r"[1-9][0-9]*", value) for value in values):
        raise RuntimeError("Missing GitHub run identity")
    name = "openai-cli-wsl1-" + "-".join(values)
    temporary = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    directory = temporary / name
    if directory.is_symlink():
        raise RuntimeError("WSL state directory is a link")
    return name, directory


def remaining_deadline(key):
    value = datetime.fromisoformat(os.environ[key])
    if value.tzinfo is None:
        raise RuntimeError("WSL deadline has no timezone")
    remaining = (value - datetime.now(timezone.utc)).total_seconds()
    if remaining <= 0:
        raise RuntimeError("WSL job deadline has expired")
    return time.monotonic() + remaining


def clean_distro(commands, wsl, name, state):
    if state.get("import_attempted"):
        # Import can register a distro before returning an error. Always reconcile it.
        names = distribution_names(commands.run("cleanup-inventory", [wsl, "--list", "--quiet"], 20, cleanup=True)[0])
        if name in names:
            commands.run("terminate", [wsl, "--terminate", name], 30, cleanup=True, expected=None)
            commands.run("unregister", [wsl, "--unregister", name], 60, cleanup=True)
        final = distribution_names(commands.run("final-inventory", [wsl, "--list", "--quiet"], 20, cleanup=True)[0])
        if final != state["initial_distributions"]:
            raise RuntimeError("Distribution inventory did not return to its initial state")
        installed = Path(state["directory"]) / "distro"
        if installed.is_symlink() or (hasattr(installed, "is_junction") and installed.is_junction()):
            raise RuntimeError("Owned installation directory became a link")
        if installed.exists():
            shutil.rmtree(installed)
        state["distribution_absent"] = True
    image = Path(state["directory"]) / "ubuntu.wsl"
    if image.exists():
        image.unlink()


def write_json(path, data):
    path.write_text(json.dumps(data, indent=2) + "\n", encoding="utf-8")


def copy_payload(commands, root, user, binary, target, expected_hash, prefix=""):
    translated = commands.run(prefix + "binary-path", root + ["/usr/bin/wslpath", "-u", str(binary)], 20)[0].decode("utf-8").strip()
    if not translated.startswith("/mnt/") or "\n" in translated:
        raise RuntimeError("Unexpected Windows payload translation")
    commands.run(prefix + "copy", root + ["/usr/bin/install", "-m", "700", "-o", "cli-compat", "-g", "cli-compat", "--", translated, target], 30)
    copied = commands.run(prefix + "binary-hash", user + ["/usr/bin/sha256sum", target], 10)[0].decode("ascii").split()[0]
    if copied != expected_hash:
        raise RuntimeError("The copied Linux binary changed")


def test_names(cases):
    return set(cases) | {root + "/" + child for root, children in cases.items() for child in children}


def load_command_cases():
    """Load the fixed supplemental contracts without affecting standalone cleanup."""
    def unique_object(pairs):
        value = dict(pairs)
        if len(value) != len(pairs):
            raise RuntimeError("Duplicated WSL command-contract inventory key")
        return value

    if COMMAND_CASES_PATH.is_symlink() or not COMMAND_CASES_PATH.is_file():
        raise RuntimeError("Expected a regular WSL command-contract inventory")
    data = COMMAND_CASES_PATH.read_bytes()
    suites = json.loads(data, object_pairs_hook=unique_object)
    if not isinstance(suites, dict) or set(suites) != {"custom", "images", "process", "app"}:
        raise RuntimeError("Invalid WSL command-contract inventory")
    excluded = set(PROCESS_CASES) | {ROOT, "TestMainDispatchProcess", "TestMainWrappedExitCodeProcess",
                                      "TestRequestConfigurationChildEnvironment"}
    selected = set()
    for package, roots, names in (("custom", 4, 9), ("images", 6, 62), ("process", 65, 577), ("app", 2, 12)):
        cases = suites[package]
        if not isinstance(cases, dict) or len(cases) != roots:
            raise RuntimeError("Invalid WSL command-contract root inventory")
        for root, children in cases.items():
            if (not re.fullmatch(r"Test[A-Za-z0-9_]+", root) or root in excluded or root in selected or
                    not isinstance(children, list) or
                    any(not isinstance(child, str) or not child or re.search(r"\s", child) for child in children) or
                    len(children) != len(set(children))):
                raise RuntimeError("Invalid or duplicated WSL command-contract case")
        selected.update(cases)
        if len(test_names(cases)) != names:
            raise RuntimeError("Invalid WSL command-contract named inventory")
    return suites, hashlib.sha256(data).hexdigest()


def load_local_cases():
    def unique_object(pairs):
        result = dict(pairs)
        if len(result) != len(pairs):
            raise RuntimeError("Duplicated local-state inventory key")
        return result

    if LOCAL_CASES_PATH.is_symlink() or not LOCAL_CASES_PATH.is_file():
        raise RuntimeError("Expected a regular local-state inventory")
    raw = LOCAL_CASES_PATH.read_bytes()
    suites = json.loads(raw, object_pairs_hook=unique_object)
    counts = {"imageprefs": (9, 30), "custom": (12, 29), "autocomplete": (5, 59),
              "process": (29, 147), "clihelp": (5, 6)}
    if not isinstance(suites, dict) or set(suites) != set(counts):
        raise RuntimeError("Invalid local-state packages")
    excluded = set(PROCESS_CASES) | {ROOT, "TestMainDispatchProcess", "TestImagePreferenceWaitingWriterProcess"}
    previous, _ = load_command_cases()
    for cases in previous.values():
        excluded.update(cases)
    selected = set()
    for package, (roots, names) in counts.items():
        cases = suites[package]
        if not isinstance(cases, dict) or len(cases) != roots:
            raise RuntimeError("Invalid local-state root count")
        for root, children in cases.items():
            if (not re.fullmatch(r"Test[A-Za-z0-9_]+", root) or root in excluded or root in selected
                    or not isinstance(children, list)
                    or any(not isinstance(child, str) or not child or re.search(r"\s", child) for child in children)
                    or len(children) != len(set(children))):
                raise RuntimeError("Invalid or duplicated local-state case")
            for child in children:
                if "<loopback>" in child and root + "/" + child != WELCOME_DYNAMIC_NAME:
                    raise RuntimeError("Unexpected dynamic loopback inventory slot")
        selected.update(cases)
        if len(test_names(cases)) != names:
            raise RuntimeError("Invalid local-state named inventory")
    if WELCOME_DYNAMIC_NAME not in test_names(suites["process"]):
        raise RuntimeError("Missing dynamic loopback inventory slot")
    return suites, hashlib.sha256(raw).hexdigest()


def admit_profile(commands, phases):
    if commands.deadline - time.monotonic() < sum(phases.values()):
        raise RuntimeError("Insufficient work time for the complete remaining WSL profile")


def require_capability_receipt(stdout, contract):
    lines = [line.removeprefix("PICKER_CAPABILITY ") for line in stdout.decode("utf-8").splitlines()
             if line.startswith("PICKER_CAPABILITY ")]
    if len(lines) != 1:
        raise RuntimeError("Missing or duplicated native capability receipt")
    def unique_object(pairs):
        value = dict(pairs)
        if len(value) != len(pairs):
            raise RuntimeError("Duplicated native capability receipt key")
        return value
    value = json.loads(lines[0], object_pairs_hook=unique_object)
    errnos = {""} if contract == "supported" else {"ENOSYS", "EOPNOTSUPP", "EINVAL"}
    if (contract not in {"supported", "refused"} or not isinstance(value, dict)
            or set(value) != {"contract", "errno", "checks"} or value["contract"] != contract
            or not isinstance(value["errno"], str) or value["errno"] not in errnos or type(value["checks"]) is not int or value["checks"] != 10):
        raise RuntimeError("Invalid native capability receipt")
    return value


def shell_prerequisites(commands, user):
    # This uses only the guest standard library. It installs nothing.
    code = r'''
import fcntl, hashlib, json, locale, os, pathlib, pty, select, signal, struct, subprocess, sys, termios
assert sys.version_info >= (3, 8), "Python 3.8 or newer is required"
assert os.getuid() == 2000 and os.uname().machine == "x86_64"
assert all(hasattr(os, name) for name in ("waitid", "WNOWAIT", "P_PID", "WEXITED", "WNOHANG")), "owned child status is unavailable"
bash = pathlib.Path("/usr/bin/bash").resolve(strict=True)
python = pathlib.Path(sys.executable).resolve(strict=True)
def identity(path):
    h = hashlib.sha256()
    with path.open("rb") as source:
        header = source.read(20)
        assert header[:6] == b"\x7fELF\x02\x01" and header[18:20] == b"\x3e\x00"
        h.update(header)
        for chunk in iter(lambda: source.read(1048576), b""):
            h.update(chunk)
    return {"path": str(path), "sha256": h.hexdigest()}
assert locale.getpreferredencoding(False).lower().replace("-", "") == "utf8"
assert pathlib.Path("/dev/ptmx").exists() and pathlib.Path("/dev/pts").is_dir()
master, slave = pty.openpty()
child = None
try:
    child = subprocess.Popen([str(bash), "--noprofile", "--norc", "-c", "test -t 0 && test -t 1"],
        stdin=slave, stdout=slave, stderr=slave, start_new_session=True,
        preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
    assert child.wait(timeout=5) == 0
finally:
    if child is not None and child.poll() is None:
        os.killpg(child.pid, signal.SIGKILL)
    os.close(master)
    os.close(slave)
    if child is not None:
        child.wait(timeout=5)
print(json.dumps({"bash": identity(bash), "python": identity(python), "python_version": sys.version,
                  "utf8": True, "controlling_pty": True, "uid": os.getuid()}))
'''
    stdout, stderr, _ = commands.run("shell-prerequisites", user + ["/usr/bin/python3", "-I", "-B", "-c", code], 25)
    if stderr:
        raise RuntimeError("Shell prerequisite check wrote stderr")
    return json.loads(stdout)


def profile_file_hashes(commands, user, paths, label, seconds=10):
    # Payload ownership applies only to the private copies, not system interpreters.
    script = ('set -eu; for file do test -f "$file" && test ! -L "$file" && test -x "$file"; '
              'case "$file" in /home/cli-compat/payload/*) '
              'test "$(stat -c %u:%g:%a -- "$file")" = 2000:2000:700 ;; esac; done; '
              'exec /usr/bin/sha256sum -- "$@"')
    stdout, stderr, _ = commands.run(label, user + ["/bin/bash", "--noprofile", "--norc", "-c",
                                                   script, "profile-files", *paths], seconds)
    rows = [line.split("  ", 1) for line in stdout.decode("ascii").splitlines()]
    if stderr or len(rows) != len(paths) or any(len(row) != 2 for row in rows):
        raise RuntimeError("Invalid profile artifact metadata or hash output")
    return {path: value for value, path in rows}


@dataclass(frozen=True)
class Payload:
    filename: str
    path: Path
    result_path: tuple
    copy_prefix: str
    executable: bool = True


@dataclass(frozen=True)
class ProfilePhase:
    label: str
    seconds: int
    result_path: tuple
    arguments: object
    validate: object
    budget: str = None
    closes_budget: bool = True
    shared_budget: bool = False
    cases: object = None
    verify_payload: str = None


@dataclass(frozen=True)
class ProfilePlan:
    selection: str
    scope: str
    payloads: tuple = ()
    phases: tuple = ()
    budgets: dict = field(default_factory=dict)
    initial_result: dict = field(default_factory=dict)
    completion_result: dict = field(default_factory=dict)
    inventory: tuple = ()
    prepare: object = None
    check_copied_artifacts: bool = False
    final_guest_paths: object = None
    cleanup_only: bool = False


class ProfileOptionsError(RuntimeError):
    """Reject an invalid CLI payload combination before lifecycle admission."""


def set_result(result, path, value):
    for key in path[:-1]:
        result = result.setdefault(key, {})
    result[path[-1]] = value


def test_arguments(hashes, result, *, filename, selector, seconds, setup_contract, native):
    go_seconds, guest_seconds = (150, 160) if seconds == 170 else (45, 60)
    env = [] if setup_contract is None else ["OPENAI_CLI_PICKER_SETUP_CONTRACT=" + setup_contract]
    if native:
        env += ["OPENAI_CLI_REQUIRE_NATIVE_SHELLS=bash",
                "OPENAI_CLI_NATIVE_SHELL_BINARY=" + PAYLOAD_ROOT + "openai",
                "OPENAI_CLI_NATIVE_SHELL_SHA256=" + hashes["openai"]]
    return env + ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", f"{guest_seconds}s",
                  PAYLOAD_ROOT + filename, "-test.run=" + selector, f"-test.timeout={go_seconds}s", "-test.v"]


def validate_test_phase(stdout, stderr, code, *, cases, label, entry, setup_contract):
    names = sorted(require_test_pass(stdout, stderr, code, test_names(cases), label,
                                    WELCOME_DYNAMIC_NAME in test_names(cases)))
    if not entry:
        return names
    value = {"tests": names, "final_PASS": 1}
    if "TestPickerNativeSetupContract" in cases:
        value["native_capability"] = require_capability_receipt(stdout, setup_contract or "supported")
    return value


def test_phase(label, filename, cases, seconds, result_path, *, selector=None, setup_contract=None,
               native=False, budget=None, closes_budget=True, shared_budget=False, verify_payload=None):
    cases = {root: tuple(children) for root, children in cases.items()}
    return ProfilePhase(
        label, seconds, result_path,
        partial(test_arguments, filename=filename, selector=selector or "^(" + "|".join(cases) + ")$",
                seconds=seconds, setup_contract=setup_contract, native=native),
        partial(validate_test_phase, cases=cases, label=label, entry=len(result_path) > 1,
                setup_contract=setup_contract),
        budget, closes_budget, shared_budget, cases, verify_payload)


def shell_arguments(hashes, result, *, script, args, guest_seconds):
    return ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", f"{guest_seconds}s",
            "/usr/bin/python3", "-I", "-B", PAYLOAD_ROOT + script,
            PAYLOAD_ROOT + "openai", result["shell_prerequisites"]["bash"]["path"], *args]


def load_shell_receipts():
    spec = importlib.util.spec_from_file_location(
        "wsl_shell_receipts", Path(__file__).with_name("wsl_shell_receipts.py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def require_driver_pass(stdout, stderr, code, names, receipt_case=None):
    # The fixture entrypoint tests use the same lazy validation boundary.
    return load_shell_receipts().require_driver_pass(stdout, stderr, code, names, receipt_case)


def shell_phase(label, script, args, seconds, guest_seconds, names, receipt=None, *, validator):
    return ProfilePhase(
        label, seconds, ("profile_results", label),
        partial(shell_arguments, script=script, args=tuple(args), guest_seconds=guest_seconds),
        partial(validator, names=frozenset(names), receipt_case=receipt),
        budget=label)


def prepare_shell(commands, user, result):
    result["shell_prerequisites"] = shell_prerequisites(commands, user)


def shell_final_paths(result):
    return {result["shell_prerequisites"][key]["path"]: result["shell_prerequisites"][key]["sha256"]
            for key in ("bash", "python")}


def fixed_payloads(payloads):
    return tuple(Payload(name, path, ("artifact_sha256", name), name + "-") for name, path in payloads.items())


def local_state_plan(payloads, setup_contract):
    suites, inventory_hash = load_local_cases()
    independent = {root: children for root, children in suites["process"].items()
                   if root in INDEPENDENT_PROCESS_ROOTS}
    if set(independent) != INDEPENDENT_PROCESS_ROOTS:
        raise RuntimeError("Missing independent local process roots")
    custom_independent = {root: children for root, children in suites["custom"].items()
                          if root != NATIVE_CUSTOM_ROOT}
    autocomplete = {root: children for root, children in suites["autocomplete"].items()
                    if setup_contract == "supported" or root not in SUPPORTED_AUTOCOMPLETE_ROOTS}
    dependent = {root: children for root, children in suites["process"].items()
                 if root not in INDEPENDENT_PROCESS_ROOTS
                 and (setup_contract == "supported" or root == NATIVE_PROCESS_ROOT)}
    groups = (
        ("imageprefs", "imageprefs", suites["imageprefs"], True),
        ("clihelp", "clihelp", suites["clihelp"], True),
        ("custom-independent", "custom", custom_independent, False),
        ("process-independent", "process", independent, False),
        ("autocomplete", "autocomplete", autocomplete, True),
        ("custom", "custom", {NATIVE_CUSTOM_ROOT: suites["custom"][NATIVE_CUSTOM_ROOT]}, True),
        ("process", "process", dependent, True),
    )
    budgets = dict(PROFILE_PHASES["local-state"])
    phases = tuple(test_phase("local-" + key, group + ".test", cases, budgets[group],
                              ("profile_results", key), setup_contract=setup_contract,
                              budget=group, closes_budget=complete, shared_budget=True)
                   for key, group, cases, complete in groups)
    omitted = [] if setup_contract == "supported" else sorted(
        test_names({root: suites["autocomplete"][root] for root in SUPPORTED_AUTOCOMPLETE_ROOTS})
        | test_names({root: children for root, children in suites["process"].items()
                      if root not in INDEPENDENT_PROCESS_ROOTS and root != NATIVE_PROCESS_ROOT}))
    return ProfilePlan(
        "local-state", "Ubuntu WSL 1 AMD64 local-state", fixed_payloads(payloads), phases, budgets,
        {"local_cases_sha256": inventory_hash, "setup_contract": setup_contract, "profile_results": {}},
        {"positive_installation_tests_executed": setup_contract == "supported",
         "positive_installation_tests_not_selected": omitted},
        ((LOCAL_CASES_PATH, inventory_hash, "The local-state inventory changed"),),
        check_copied_artifacts=True)


def shell_contract_plan(payloads, setup_contract):
    validator = load_shell_receipts().require_driver_pass
    assets = []
    for name in SHELL_ASSETS:
        path = Path(__file__).with_name(name)
        if path.is_symlink() or not path.is_file():
            raise RuntimeError("Expected a regular shell fixture source")
        assets.append(Payload(name, path, ("artifact_sha256", name), name + "-", False))
    phases = [
        test_phase("invocation", "process.test", NATIVE_CASES, 170, ("profile_results", "invocation"),
                   selector=NATIVE_SELECTOR, native=True, budget="invocation"),
        shell_phase("completion", "check-bash-completion.py", (), 75, 65, COMPLETION_CASES, validator=validator),
    ]
    for mode in ("login", "nonlogin"):
        args, receipt = [mode], "bash-" + mode
        if setup_contract == "refused":
            args += ["--setup-contract", "refused", "--autocomplete-test-binary", PAYLOAD_ROOT + "autocomplete.test"]
            receipt += "-refused"
            if mode == "nonlogin":
                args.append("--first-use-admission")
        phases.append(shell_phase("startup-" + mode, "check-wsl-shell-startup.py", args, 130, 120,
                                  {receipt}, receipt, validator=validator))
    phases.append(shell_phase("models", "check-wsl-models.py", (), 40, 30,
                              {"models-filter-sort-limit"}, "models-filter-sort-limit", validator=validator))
    return ProfilePlan(
        "shell-contracts", "Ubuntu WSL 1 AMD64 shell-contracts", fixed_payloads(payloads) + tuple(assets),
        tuple(phases), dict(PROFILE_PHASES["shell-contracts"]),
        {"setup_contract": setup_contract, "profile_results": {}},
        prepare=prepare_shell, check_copied_artifacts=True, final_guest_paths=shell_final_paths)


def legacy_plan(binary, process_binary, command_contracts, app_binary, images_binary):
    selection, scope = "pager-and-process", "Ubuntu WSL 1 AMD64 pager process"
    inventory, metadata = (), {}
    cases = {ROOT: ["LookPath", "ForkExec"]}
    payloads = [Payload("custom.test", binary, ("binary_sha256",), "")]
    if command_contracts:
        suites, inventory_hash = load_command_cases()
        cases = suites["custom"]
        selection = "command-contracts"
        scope = "Ubuntu WSL 1 AMD64 request, command, output, error, and saved-setting contracts"
        metadata["command_cases_sha256"] = inventory_hash
        inventory = ((COMMAND_CASES_PATH, inventory_hash, "The command-contract inventory changed"),)
    phases = [test_phase("custom-contracts" if command_contracts else "pager", "custom.test", cases,
                         70, ("tests",), selector=None if command_contracts else "^" + ROOT + "$")]
    if process_binary is not None:
        payloads.append(Payload("process.test", process_binary, ("process_binary_sha256",), "process-"))
        phases.append(test_phase("process-contracts" if command_contracts else "process", "process.test",
                                 suites["process"] if command_contracts else PROCESS_CASES, 170,
                                 ("process_tests",), verify_payload="process.test"))
        if not command_contracts:
            scope += "; public stdin, download, and output processes"
    if command_contracts:
        payloads += [Payload("app.test", app_binary, ("app_binary_sha256",), "app-"),
                     Payload("images.test", images_binary, ("images_binary_sha256",), "images-")]
        phases += [test_phase("images-contracts", "images.test", suites["images"], 70, ("images_tests",),
                              verify_payload="images.test"),
                   test_phase("app-contracts", "app.test", suites["app"], 70, ("app_tests",),
                              verify_payload="app.test")]
    return ProfilePlan(selection, scope, tuple(payloads), tuple(phases),
                       initial_result=metadata, inventory=inventory)


def resolve_profile(binary=None, cleanup_only=False, process_binary=None, command_contracts=False,
                    app_binary=None, images_binary=None, profile=None, extra_payloads=None, setup_contract="supported"):
    """Resolve CLI selection, payloads, phases, and validators before distro ownership."""
    if setup_contract not in {"supported", "refused"} or (profile is None and setup_contract != "supported"):
        raise ProfileOptionsError("Setup contracts require a fixed WSL profile")
    if profile is not None and profile not in PROFILE_PHASES:
        raise ProfileOptionsError("Unknown fixed WSL profile")
    if profile is not None and command_contracts:
        raise ProfileOptionsError("WSL profiles cannot be combined")
    selection = profile or ("command-contracts" if command_contracts else "pager-and-process")
    if cleanup_only:
        scope = "Ubuntu WSL 1 AMD64 " + profile if profile else "Ubuntu WSL 1 AMD64 pager process"
        return ProfilePlan(selection, scope, cleanup_only=True)
    extras = dict(extra_payloads or {})
    if profile is None:
        if extras:
            raise ProfileOptionsError("Additional binaries require a fixed successor profile")
        if command_contracts and any(path is None for path in (binary, process_binary, app_binary, images_binary)):
            raise ProfileOptionsError("Command contracts require all four test binaries")
        if binary is None:
            raise ProfileOptionsError("--binary is required for the pilot")
        return legacy_plan(binary, process_binary, command_contracts, app_binary, images_binary)
    if app_binary is not None or images_binary is not None:
        raise ProfileOptionsError("Application and image binaries require the command-contract profile")
    if binary is not None:
        extras["custom.test"] = binary
    if process_binary is not None:
        extras["process.test"] = process_binary
    required, factory = {
        "local-state": ({"custom.test", "process.test", "imageprefs.test", "clihelp.test", "autocomplete.test"}, local_state_plan),
        "shell-contracts": ({"process.test", "openai"} | ({"autocomplete.test"} if setup_contract == "refused" else set()),
                            shell_contract_plan),
    }[profile]
    if set(extras) != required:
        raise ProfileOptionsError("The fixed WSL profile requires its exact payload set")
    return factory(extras, setup_contract)


def run_profile(commands, user, plan, hashes, result):
    """Execute a resolved plan without selecting a profile during the lifecycle."""
    for key, value in plan.initial_result.items():
        result.setdefault(key, copy.deepcopy(value))
    remaining = dict(plan.budgets)
    if remaining:
        admit_profile(commands, remaining)
        result["profile_admission_seconds"] = sum(remaining.values())
    payloads = {payload.filename: payload for payload in plan.payloads}
    for phase in plan.phases:
        if remaining:
            admit_profile(commands, remaining)
        elif commands.deadline - time.monotonic() < phase.seconds:
            raise RuntimeError("Insufficient work time for the WSL " + phase.label + " suite")
        deadline, started = commands.deadline, time.monotonic()
        if phase.shared_budget:
            commands.deadline = min(deadline, started + remaining[phase.budget])
        try:
            stdout, stderr, code = commands.run(phase.label, user + phase.arguments(hashes, result), phase.seconds)
            accepted = phase.validate(stdout, stderr, code)
        finally:
            if phase.shared_budget:
                remaining[phase.budget] -= time.monotonic() - started
                commands.deadline = deadline
        if phase.shared_budget and remaining[phase.budget] <= 0:
            raise RuntimeError("Local checks exhausted their shared time budget")
        set_result(result, phase.result_path, accepted)
        if phase.verify_payload and digest(payloads[phase.verify_payload].path) != hashes[phase.verify_payload]:
            raise RuntimeError("The host " + phase.verify_payload.removesuffix(".test") + " test binary changed")
        if phase.budget and phase.closes_budget:
            del remaining[phase.budget]
    result.update(plan.completion_result)
    if remaining:
        admit_profile(commands, remaining)
    return remaining


def pilot(plan):
    cleanup_only = plan.cleanup_only
    artifact_hashes, payload_paths = {}, {}
    # Validate process bitness before selecting System32, which WOW64 can redirect.
    host_process = require_native_windows_process()
    name, directory = owned_paths()
    wsl = str(Path(os.environ["SYSTEMROOT"]) / "System32" / "wsl.exe")
    state_path = directory / "state.json"
    if cleanup_only and not state_path.exists():
        return 0
    if cleanup_only:
        state = json.loads(state_path.read_text(encoding="utf-8"))
        if state["name"] != name or state["directory"] != str(directory):
            raise RuntimeError("WSL cleanup ownership mismatch")
    else:
        directory.mkdir(mode=0o700)
        state = {"name": name, "directory": str(directory), "import_attempted": False,
                 "initial_distributions": [], "distribution_absent": False}
        write_json(state_path, state)
    start = time.monotonic()
    cleanup_deadline = remaining_deadline("WSL_PAGER_CLEANUP_DEADLINE")
    work_deadline = cleanup_deadline if cleanup_only else remaining_deadline("WSL_PAGER_WORK_DEADLINE")
    commands = Commands(directory, min(work_deadline, start + (150 if cleanup_only else 780)), "final-" if cleanup_only else "")
    result = {"state": "failed", "scope": plan.scope, "selection": plan.selection, "commands": commands.records}
    result.update(copy.deepcopy(plan.initial_result))
    result["requires_successful_process_exit"] = True
    result["host_process"] = host_process
    result["runner_image"] = {key: os.environ.get(key) for key in ("ImageOS", "ImageVersion", "RUNNER_ARCH")}
    failure = None
    try:
        if not cleanup_only:
            for payload in plan.payloads:
                value = require_linux_binary(payload.path) if payload.executable else digest(payload.path)
                artifact_hashes[payload.filename] = value
                payload_paths[payload.filename] = payload.path.resolve(strict=True)
                set_result(result, payload.result_path, value)
            result["wsl_sha256"] = digest(Path(wsl))
            result["source"] = commands.run("source", ["git", "rev-parse", "HEAD", "HEAD^{tree}"], 10)[0].decode("ascii").splitlines()
            result["wsl_version"] = decode_metadata(commands.run("wsl-version", [wsl, "--version"], 20)[0])
            initial = distribution_names(commands.run("initial-inventory", [wsl, "--list", "--quiet"], 20)[0])
            if initial:
                raise RuntimeError("The disposable runner already has a distribution")
            dism = Path(os.environ["SYSTEMROOT"]) / "System32" / "dism.exe"
            result["dism_sha256"] = digest(dism)
            enabled, errors, _ = commands.run("feature", [str(dism), "/Online", "/Get-FeatureInfo",
                "/FeatureName:Microsoft-Windows-Subsystem-Linux", "/English", "/NoRestart", "/LogLevel:1"], 90)
            require_feature_enabled(enabled, errors)
            if shutil.disk_usage(directory).free < 6 * 1024**3:
                raise RuntimeError("Insufficient free space for the disposable distro")
            image = directory / "ubuntu.wsl"
            result["image_open_attempts"] = []
            staged = os.environ.get("WSL_PAGER_IMAGE")
            result["image_transport"] = "artifact" if staged else "https"
            result["image_bytes"] = download_image(
                image, commands.deadline, result["image_open_attempts"],
                staged_image=Path(staged) if staged else None)
            result["image_sha256"] = IMAGE_SHA256
            state["import_attempted"] = True
            write_json(state_path, state)
            commands.run("import", [wsl, "--import", name, str(directory / "distro"), str(image), "--version", "1"], 180)
            require_version_one(commands.run("version-one", [wsl, "--list", "--verbose"], 20)[0], name)
            root = [wsl, "--distribution", name, "--user", "root", "--exec", "/usr/bin/env", "-i", "PATH=/usr/bin:/bin", "LC_ALL=C.UTF-8"]
            setup = ("set -eu; if getent passwd 2000 >/dev/null || getent passwd cli-compat >/dev/null || getent group cli-compat >/dev/null; then exit 1; fi; "
                     "/usr/sbin/useradd --create-home --uid 2000 --user-group --shell /bin/bash cli-compat; "
                     "install -d -m 700 -o cli-compat -g cli-compat /home/cli-compat/payload")
            commands.run("user", root + ["/bin/bash", "--noprofile", "--norc", "-c", setup], 30)
            user = [wsl, "--distribution", name, "--user", "cli-compat", "--exec", "/usr/bin/env", "-i",
                    "HOME=/home/cli-compat", "TMPDIR=/home/cli-compat", "PATH=/usr/bin:/bin", "LC_ALL=C.UTF-8",
                    "TERM=dumb", "NO_COLOR=1", "GOMAXPROCS=2"]
            for payload in plan.payloads:
                copy_payload(commands, root, user, payload_paths[payload.filename], PAYLOAD_ROOT + payload.filename,
                             artifact_hashes[payload.filename], payload.copy_prefix)
            identity = "set -eu; test $(id -u) = 2000; test $(uname -m) = x86_64; test $(dpkg --print-architecture) = amd64; cat /etc/os-release; uname -a; id; /bin/bash --version; stat -f -c %T /home/cli-compat"
            observed = commands.run("ubuntu-identity", user + ["/bin/bash", "--noprofile", "--norc", "-c", identity], 20)[0].decode("utf-8")
            if not re.search(r"^ID=ubuntu$", observed, re.MULTILINE):
                raise RuntimeError("The imported userspace is not Ubuntu")
            result["identity"] = observed
            control = "printf 'wsl-control-out\\n'; printf 'wsl-control-err\\n' >&2; exit 7"
            stdout, stderr, _ = commands.run("exit-control", user + ["/bin/bash", "--noprofile", "--norc", "-c", control], 10, expected=7)
            if stdout != b"wsl-control-out\n" or stderr != b"wsl-control-err\n":
                raise RuntimeError("WSL stream or exit control changed")
            if plan.check_copied_artifacts:
                commands.run("profile-tools", user + ["/bin/bash", "--noprofile", "--norc", "-c",
                    "test -x /usr/bin/timeout && test -x /usr/bin/mkfifo && test -x /usr/bin/sha256sum"], 10)
                copied = {PAYLOAD_ROOT + name: value for name, value in artifact_hashes.items()}
                if profile_file_hashes(commands, user, list(copied), "profile-prepared-hashes") != copied:
                    raise RuntimeError("A prepared profile artifact changed")
            if plan.prepare is not None:
                plan.prepare(commands, user, result)
            remaining = run_profile(commands, user, plan, artifact_hashes, result)
            if remaining:
                # Keep final source and artifact verification inside its reserved phase.
                commands.deadline = min(commands.deadline, time.monotonic() + remaining["final"])
            if plan.check_copied_artifacts:
                expected = {PAYLOAD_ROOT + name: value for name, value in artifact_hashes.items()}
                if plan.final_guest_paths is not None:
                    expected.update(plan.final_guest_paths(result))
                if profile_file_hashes(commands, user, list(expected), "profile-final-hashes") != expected:
                    raise RuntimeError("A copied profile artifact changed")
            for filename, path in payload_paths.items():
                if digest(path) != artifact_hashes[filename]:
                    raise RuntimeError("A host profile artifact changed")
            for path, expected_hash, error in plan.inventory:
                if digest(path) != expected_hash:
                    raise RuntimeError(error)
            final_source = commands.run("source-final", ["git", "rev-parse", "HEAD", "HEAD^{tree}"], 10)[0].decode("ascii").splitlines()
            if final_source != result["source"]:
                raise RuntimeError("The source commit changed during execution")
    except Exception as error:
        failure = str(error)
    finally:
        commands.deadline = min(cleanup_deadline, start + (150 if cleanup_only else 930), time.monotonic() + 150)
        try:
            clean_distro(commands, wsl, name, state)
        except Exception as error:
            result["cleanup_error"] = str(error)
        try:
            commands.finish()
        except Exception as error:
            result["cleanup_error"] = "; ".join(filter(None, [result.get("cleanup_error"), str(error)]))
        if time.monotonic() >= commands.deadline:
            result["cleanup_error"] = "; ".join(filter(None, [result.get("cleanup_error"), "Cleanup deadline exceeded"]))
        result.update(error=failure, elapsed_seconds=round(time.monotonic() - start, 3), signal=STOP)
        if not failure and not result.get("cleanup_error") and not STOP:
            result["state"] = "passed"
        write_json(state_path, state)
        write_json(directory / ("cleanup.json" if cleanup_only else "result.json"), result)
    if time.monotonic() >= commands.deadline:
        return 128 + STOP if STOP else 1
    return 128 + STOP if STOP else (0 if result["state"] == "passed" else 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--process-binary", type=Path, help="Also run the fixed noninteractive process suite")
    parser.add_argument("--images-binary", type=Path, help="Run the image preference and state contracts")
    parser.add_argument("--app-binary", type=Path, help="Run the application lifecycle contracts")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--command-contracts", action="store_true",
                           help="Run the fixed command-contract supplement using all four test binaries")
    selection.add_argument("--profile", choices=sorted(PROFILE_PHASES), help="Run one fixed successor profile")
    parser.add_argument("--imageprefs-binary", type=Path)
    parser.add_argument("--clihelp-binary", type=Path)
    parser.add_argument("--autocomplete-binary", type=Path)
    parser.add_argument("--cli-binary", type=Path)
    parser.add_argument("--setup-contract", choices=("supported", "refused"), default="supported")
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    extras = {name: path for name, path in (("imageprefs.test", args.imageprefs_binary),
              ("clihelp.test", args.clihelp_binary), ("autocomplete.test", args.autocomplete_binary),
              ("openai", args.cli_binary)) if path is not None}
    for number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(number, interrupted)
    try:
        plan = resolve_profile(args.binary, args.cleanup, args.process_binary, args.command_contracts,
                               args.app_binary, args.images_binary, args.profile, extras, args.setup_contract)
    except ProfileOptionsError as error:
        parser.error(str(error))
    except Exception as error:
        print(f"WSL pager pilot failed: {error}", file=sys.stderr)
        return 128 + STOP if STOP else 1
    try:
        code = pilot(plan)
    except Exception as error:
        print(f"WSL pager pilot failed: {error}", file=sys.stderr)
        code = 128 + STOP if STOP else 1
    return code


if __name__ == "__main__":
    raise SystemExit(main())
