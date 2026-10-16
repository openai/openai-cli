#!/usr/bin/env python3
"""Run fixed compatibility checks in one disposable Ubuntu WSL 1 distro.

This is a process check. It does not test Windows Terminal or WSL 2.
The calling workflow cross-compiles the test binary before importing Ubuntu.
"""

import argparse
import base64
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


IMAGE_URL = "https://releases.ubuntu.com/24.04.5/ubuntu-24.04.5-wsl-amd64.wsl"
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
PROFILE_PHASES = {
    "local-state": {"imageprefs": 70, "custom": 70, "process": 170, "clihelp": 70, "final": 20},
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
        if not newurl.startswith("https://releases.ubuntu.com/"):
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


def download_image(path, deadline, attempts=None):
    # A socket read can take ten seconds. Reserve that time inside the 480-second cap.
    end = min(deadline, time.monotonic() + 470)
    hasher, size = hashlib.sha256(), 0
    opener = urllib.request.build_opener(HTTPSOnly())
    with open_image(opener, end, attempts if attempts is not None else []) as response, path.open("xb") as output:
        if int(response.headers.get("Content-Length", "0")) > IMAGE_LIMIT:
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
    if not isinstance(suites, dict) or set(suites) != {"custom", "process", "app"}:
        raise RuntimeError("Invalid WSL command-contract inventory")
    excluded = set(PROCESS_CASES) | {ROOT, "TestMainDispatchProcess", "TestMainWrappedExitCodeProcess",
                                      "TestRequestConfigurationChildEnvironment"}
    selected = set()
    for package, roots, names in (("custom", 10, 71), ("process", 65, 577), ("app", 2, 12)):
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


def run_custom_suite(commands, user, target, cases=None, *, label=None):
    # Admit the complete custom stage before launching either fixed selection.
    if commands.deadline - time.monotonic() < 70:
        raise RuntimeError("Insufficient work time for the WSL custom suite")
    label = label or ("pager" if cases is None else "custom-contracts")
    tests = TESTS if cases is None else test_names(cases)
    selector = "^" + ROOT + "$" if cases is None else "^(" + "|".join(cases) + ")$"
    test = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "60s", target,
            "-test.run=" + selector, "-test.timeout=45s", "-test.v"]
    stdout, stderr, code = commands.run(label, user + test, 70)
    require_test_pass(stdout, stderr, code, tests, label)
    return tests


def run_process_suite(commands, user, target, cases=None):
    # Reserve the full bounded stage before launching any selected process tests.
    if commands.deadline - time.monotonic() < 170:
        raise RuntimeError("Insufficient work time for the WSL process suite")
    label = "process" if cases is None else "process-contracts"
    tests = PROCESS_TESTS if cases is None else test_names(cases)
    selector = PROCESS_SELECTOR if cases is None else "^(" + "|".join(cases) + ")$"
    test = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s", target,
            "-test.run=" + selector, "-test.timeout=150s", "-test.v"]
    stdout, stderr, code = commands.run(label, user + test, 170)
    require_test_pass(stdout, stderr, code, tests, label)
    return tests


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
    counts = {"imageprefs": (9, 30), "custom": (10, 15), "process": (28, 142), "clihelp": (3, 4)}
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


def run_selected_tests(commands, user, target, cases, label, seconds, selector=None, dynamic_loopback=False):
    go_seconds, guest_seconds = (150, 160) if seconds == 170 else (45, 60)
    selector = selector or "^(" + "|".join(cases) + ")$"
    argv = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", f"{guest_seconds}s", target,
            "-test.run=" + selector, f"-test.timeout={go_seconds}s", "-test.v"]
    stdout, stderr, code = commands.run(label, user + argv, seconds)
    return sorted(require_test_pass(stdout, stderr, code, test_names(cases), label, dynamic_loopback))


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
        expected_counts = {"setup_call_count": 6, "fresh_shell_count": 3,
                           "expected_setup_calls": 6, "expected_fresh_shells": 3}
        required_true = ("setup_stdout_eof", "setup_stderr_eof", "pty_eof", "system_sources_unchanged")
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
        if (not isinstance(calls, list) or len(calls) != 6 or not isinstance(shells, list) or len(shells) != 3
                or any(not isinstance(item, dict) for item in calls + shells)
                or [item.get("action") for item in calls] !=
                ["install", "install-again", "remove", "remove-again", "reenable", "remove-final"]
                or [item.get("checkpoint") for item in shells] !=
                ["fresh-installed", "fresh-removed", "fresh-reenabled"]):
            raise RuntimeError("Startup receipt lost a lifecycle action")
        for item in calls + shells:
            if type(item.get("exit_code")) is not int or item["exit_code"] != 0:
                raise RuntimeError("Startup receipt contains a failed command")
        if any(item.get("stdout_eof") is not True or item.get("stderr_eof") is not True for item in calls):
            raise RuntimeError("Startup receipt contains incomplete setup output")
        if any(item.get("pty_eof") is not True or item.get("forced_cleanup") is not False for item in shells):
            raise RuntimeError("Startup receipt contains incomplete fresh shells")
        for item in calls:
            for stream in ("stdout", "stderr"):
                raw = receipt_bytes(item, stream, 1048576)
                if stream == "stdout" and raw:
                    raise RuntimeError("Startup setup wrote unexpected stdout")
        for item in shells:
            raw = receipt_bytes(item, "pty", 1048576)
            if b"__PROFILE_READY__> " not in raw:
                raise RuntimeError("Startup receipt lost its prompt evidence")
    if any(type(receipt.get(key)) is not int or receipt[key] != value for key, value in expected_counts.items()):
        raise RuntimeError("WSL shell receipt has unexpected command or request counts")
    if any(receipt.get(key) is not True for key in required_true):
        raise RuntimeError("WSL shell receipt lacks complete output or cleanup")
    return receipt


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


def run_profile(commands, user, profile, suites, hashes, result):
    remaining = dict(PROFILE_PHASES[profile])
    admit_profile(commands, remaining)
    result["profile_admission_seconds"] = sum(remaining.values())
    result["profile_results"] = {}
    for phase, seconds in list(remaining.items()):
        admit_profile(commands, remaining)
        if phase == "final":
            break
        if profile == "local-state":
            names = run_selected_tests(commands, user, PAYLOAD_ROOT + phase + ".test", suites[phase],
                                       "local-" + phase, seconds, dynamic_loopback=phase == "process")
            result["profile_results"][phase] = {"tests": names, "final_PASS": 1}
        elif phase == "invocation":
            env = ["OPENAI_CLI_REQUIRE_NATIVE_SHELLS=bash",
                   "OPENAI_CLI_NATIVE_SHELL_BINARY=" + PAYLOAD_ROOT + "openai",
                   "OPENAI_CLI_NATIVE_SHELL_SHA256=" + hashes["openai"]]
            names = run_selected_tests(commands, user + env, PAYLOAD_ROOT + "process.test", NATIVE_CASES,
                                       phase, seconds, selector=NATIVE_SELECTOR)
            result["profile_results"][phase] = {"tests": names, "final_PASS": 1}
        else:
            if phase == "completion":
                script, args, guest_seconds, names, receipt = "check-bash-completion.py", [], 65, COMPLETION_CASES, None
            elif phase.startswith("startup-"):
                mode = phase.removeprefix("startup-")
                script, args, guest_seconds = "check-wsl-shell-startup.py", [mode], 120
                names, receipt = {"bash-" + mode}, "bash-" + mode
            else:
                script, args, guest_seconds = "check-wsl-models.py", [], 30
                names, receipt = {"models-filter-sort-limit"}, "models-filter-sort-limit"
            argv = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", f"{guest_seconds}s",
                    "/usr/bin/python3", "-I", "-B", PAYLOAD_ROOT + script,
                    PAYLOAD_ROOT + "openai", result["shell_prerequisites"]["bash"]["path"], *args]
            stdout, stderr, code = commands.run(phase, user + argv, seconds)
            result["profile_results"][phase] = require_driver_pass(stdout, stderr, code, names, receipt)
        del remaining[phase]
    return remaining


def pilot(binary, cleanup_only=False, process_binary=None, command_contracts=False, app_binary=None,
          profile=None, extra_payloads=None):
    suites = None
    payloads, asset_paths, artifact_hashes = {}, {}, {}
    if profile is not None and profile not in PROFILE_PHASES:
        raise RuntimeError("Unknown fixed WSL profile")
    if profile is not None and command_contracts:
        raise RuntimeError("WSL profiles cannot be combined")
    if profile is not None and not cleanup_only:
        if app_binary is not None:
            raise RuntimeError("Application lifecycle binaries require the command-contract profile")
        payloads = dict(extra_payloads or {})
        if binary is not None:
            payloads["custom.test"] = binary
        if process_binary is not None:
            payloads["process.test"] = process_binary
        required = {"custom.test", "process.test", "imageprefs.test", "clihelp.test"} if profile == "local-state" else {"process.test", "openai"}
        if set(payloads) != required:
            raise RuntimeError("The fixed WSL profile requires its exact payload set")
        if profile == "local-state":
            suites, inventory_hash = load_local_cases()
        else:
            asset_paths = {name: Path(__file__).with_name(name) for name in SHELL_ASSETS}
            for path in asset_paths.values():
                if path.is_symlink() or not path.is_file():
                    raise RuntimeError("Expected a regular shell fixture source")
    if command_contracts and not cleanup_only:
        if binary is None or process_binary is None or app_binary is None:
            raise RuntimeError("Command contracts require all three test binaries")
        suites, inventory_hash = load_command_cases()
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
    result = {"state": "failed", "scope": "Ubuntu WSL 1 AMD64 pager process", "commands": commands.records}
    result["selection"] = "command-contracts" if command_contracts else "pager-and-process"
    if command_contracts and suites is not None:
        result["scope"] = "Ubuntu WSL 1 AMD64 request, command, output, error, and saved-setting contracts"
        result["command_cases_sha256"] = inventory_hash
    if profile is not None:
        result["selection"] = profile
        result["scope"] = "Ubuntu WSL 1 AMD64 " + profile
        if profile == "local-state" and not cleanup_only:
            result["local_cases_sha256"] = inventory_hash
    result["requires_successful_process_exit"] = True
    result["host_process"] = host_process
    result["runner_image"] = {key: os.environ.get(key) for key in ("ImageOS", "ImageVersion", "RUNNER_ARCH")}
    failure = None
    try:
        if not cleanup_only:
            if profile is not None:
                artifact_hashes = {name: require_linux_binary(path) for name, path in payloads.items()}
                payloads = {name: path.resolve(strict=True) for name, path in payloads.items()}
                artifact_hashes.update({name: digest(path) for name, path in asset_paths.items()})
                result["artifact_sha256"] = artifact_hashes
            else:
                result["binary_sha256"] = require_linux_binary(binary)
                binary = binary.resolve(strict=True)
                if process_binary is not None:
                    result["process_binary_sha256"] = require_linux_binary(process_binary)
                    process_binary = process_binary.resolve(strict=True)
                    if suites is None:
                        result["scope"] += "; public stdin, download, and output processes"
                if suites is not None:
                    result["app_binary_sha256"] = require_linux_binary(app_binary)
                    app_binary = app_binary.resolve(strict=True)
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
            result["image_bytes"] = download_image(image, commands.deadline, result["image_open_attempts"])
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
            target = "/home/cli-compat/payload/custom.test"
            user = [wsl, "--distribution", name, "--user", "cli-compat", "--exec", "/usr/bin/env", "-i",
                    "HOME=/home/cli-compat", "TMPDIR=/home/cli-compat", "PATH=/usr/bin:/bin", "LC_ALL=C.UTF-8",
                    "TERM=dumb", "NO_COLOR=1", "GOMAXPROCS=2"]
            process_target = "/home/cli-compat/payload/process.test"
            app_target = "/home/cli-compat/payload/app.test"
            if profile is not None:
                for filename, path in {**payloads, **asset_paths}.items():
                    copy_payload(commands, root, user, path, PAYLOAD_ROOT + filename,
                                 artifact_hashes[filename], filename + "-")
            else:
                copy_payload(commands, root, user, binary, target, result["binary_sha256"])
                if process_binary is not None:
                    copy_payload(commands, root, user, process_binary, process_target, result["process_binary_sha256"], "process-")
                if suites is not None:
                    copy_payload(commands, root, user, app_binary, app_target, result["app_binary_sha256"], "app-")
            identity = "set -eu; test $(id -u) = 2000; test $(uname -m) = x86_64; test $(dpkg --print-architecture) = amd64; cat /etc/os-release; uname -a; id; /bin/bash --version; stat -f -c %T /home/cli-compat"
            observed = commands.run("ubuntu-identity", user + ["/bin/bash", "--noprofile", "--norc", "-c", identity], 20)[0].decode("utf-8")
            if not re.search(r"^ID=ubuntu$", observed, re.MULTILINE):
                raise RuntimeError("The imported userspace is not Ubuntu")
            result["identity"] = observed
            control = "printf 'wsl-control-out\\n'; printf 'wsl-control-err\\n' >&2; exit 7"
            stdout, stderr, _ = commands.run("exit-control", user + ["/bin/bash", "--noprofile", "--norc", "-c", control], 10, expected=7)
            if stdout != b"wsl-control-out\n" or stderr != b"wsl-control-err\n":
                raise RuntimeError("WSL stream or exit control changed")
            if profile is not None:
                commands.run("profile-tools", user + ["/bin/bash", "--noprofile", "--norc", "-c",
                    "test -x /usr/bin/timeout && test -x /usr/bin/mkfifo && test -x /usr/bin/sha256sum"], 10)
                copied = {PAYLOAD_ROOT + name: value for name, value in artifact_hashes.items()}
                if profile_file_hashes(commands, user, list(copied), "profile-prepared-hashes") != copied:
                    raise RuntimeError("A prepared profile artifact changed")
                if profile == "shell-contracts":
                    result["shell_prerequisites"] = shell_prerequisites(commands, user)
                remaining = run_profile(commands, user, profile, suites, artifact_hashes, result)
                admit_profile(commands, remaining)
                # Keep final source and artifact verification inside its reserved phase.
                commands.deadline = min(commands.deadline, time.monotonic() + remaining["final"])
                expected = {PAYLOAD_ROOT + name: value for name, value in artifact_hashes.items()}
                if profile == "shell-contracts":
                    expected.update({result["shell_prerequisites"][key]["path"]:
                                     result["shell_prerequisites"][key]["sha256"] for key in ("bash", "python")})
                if profile_file_hashes(commands, user, list(expected), "profile-final-hashes") != expected:
                    raise RuntimeError("A copied profile artifact changed")
                for filename, path in {**payloads, **asset_paths}.items():
                    if digest(path) != artifact_hashes[filename]:
                        raise RuntimeError("A host profile artifact changed")
                if profile == "local-state" and digest(LOCAL_CASES_PATH) != inventory_hash:
                    raise RuntimeError("The local-state inventory changed")
            else:
                result["tests"] = sorted(run_custom_suite(commands, user, target,
                                                         suites["custom"] if suites is not None else None))
                if process_binary is not None:
                    result["process_tests"] = sorted(run_process_suite(commands, user, process_target,
                                                                      suites["process"] if suites is not None else None))
                    if digest(process_binary) != result["process_binary_sha256"]:
                        raise RuntimeError("The host process test binary changed")
                if suites is not None:
                    result["app_tests"] = sorted(run_custom_suite(commands, user, app_target,
                                                                 suites["app"], label="app-contracts"))
                    if digest(app_binary) != result["app_binary_sha256"]:
                        raise RuntimeError("The host app test binary changed")
                if digest(binary) != result["binary_sha256"]:
                    raise RuntimeError("The host test binary changed")
                if suites is not None and digest(COMMAND_CASES_PATH) != inventory_hash:
                    raise RuntimeError("The command-contract inventory changed")
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
    parser.add_argument("--app-binary", type=Path, help="Run the application lifecycle contracts")
    selection = parser.add_mutually_exclusive_group()
    selection.add_argument("--command-contracts", action="store_true",
                           help="Run the fixed command-contract supplement using all three test binaries")
    selection.add_argument("--profile", choices=sorted(PROFILE_PHASES), help="Run one fixed successor profile")
    parser.add_argument("--imageprefs-binary", type=Path)
    parser.add_argument("--clihelp-binary", type=Path)
    parser.add_argument("--cli-binary", type=Path)
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    if not args.cleanup and args.profile != "shell-contracts" and args.binary is None:
        parser.error("--binary is required for the pilot")
    if args.command_contracts and not args.cleanup and (args.process_binary is None or args.app_binary is None):
        parser.error("--command-contracts requires --process-binary and --app-binary")
    extras = {name: path for name, path in (("imageprefs.test", args.imageprefs_binary),
              ("clihelp.test", args.clihelp_binary), ("openai", args.cli_binary)) if path is not None}
    if not args.cleanup:
        if args.profile is not None and args.app_binary is not None:
            parser.error("--app-binary cannot be combined with --profile")
        if args.profile == "local-state" and (args.process_binary is None or set(extras) != {"imageprefs.test", "clihelp.test"}):
            parser.error("--profile local-state requires process, imageprefs, and clihelp test binaries")
        if args.profile == "shell-contracts" and (args.binary is not None or args.process_binary is None or set(extras) != {"openai"}):
            parser.error("--profile shell-contracts requires only process and CLI binaries")
        if args.profile is None and extras:
            parser.error("Additional binaries require a fixed successor profile")
    for number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(number, interrupted)
    try:
        if args.profile is None:
            code = pilot(args.binary, args.cleanup, args.process_binary, args.command_contracts, args.app_binary)
        else:
            code = pilot(args.binary, args.cleanup, args.process_binary, profile=args.profile, extra_payloads=extras)
    except Exception as error:
        print(f"WSL pager pilot failed: {error}", file=sys.stderr)
        code = 128 + STOP if STOP else 1
    return code


if __name__ == "__main__":
    raise SystemExit(main())
