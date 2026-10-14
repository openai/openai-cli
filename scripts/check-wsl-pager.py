#!/usr/bin/env python3
"""Run fixed compatibility checks in one disposable Ubuntu WSL 1 distro.

This is a process check. It does not test Windows Terminal or WSL 2.
The calling workflow cross-compiles the test binary before importing Ubuntu.
"""

import argparse
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


def require_test_pass(stdout, stderr, code, tests=TESTS, label="pager"):
    text = stdout.decode("utf-8", errors="strict")
    stderr.decode("utf-8", errors="strict")
    if code != 0 or stderr:
        raise RuntimeError(f"The WSL {label} test failed or wrote stderr")
    starts = Counter(re.findall(r"^=== RUN\s+(\S+)\s*$", text, re.MULTILINE))
    passes = Counter(re.findall(r"^\s*--- PASS: (\S+) \([^\n]*\)\s*$", text, re.MULTILINE))
    if (starts != Counter(tests) or passes != Counter(tests) or
            re.search(r"--- (?:FAIL|SKIP):|panic:|fatal error:|no tests to run", text) or
            len(re.findall(r"^PASS\s*$", text, re.MULTILINE)) != 1):
        raise RuntimeError(f"Missing, skipped, duplicated, or failed {label} test execution")


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


def run_process_suite(commands, user, target):
    # Reserve the full bounded stage before launching any selected process tests.
    if commands.deadline - time.monotonic() < 170:
        raise RuntimeError("Insufficient work time for the WSL process suite")
    test = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "160s", target,
            "-test.run=" + PROCESS_SELECTOR, "-test.timeout=150s", "-test.v"]
    stdout, stderr, code = commands.run("process", user + test, 170)
    require_test_pass(stdout, stderr, code, PROCESS_TESTS, "process")


def pilot(binary, cleanup_only=False, process_binary=None):
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
    result["requires_successful_process_exit"] = True
    result["host_process"] = host_process
    result["runner_image"] = {key: os.environ.get(key) for key in ("ImageOS", "ImageVersion", "RUNNER_ARCH")}
    failure = None
    try:
        if not cleanup_only:
            result["binary_sha256"] = require_linux_binary(binary)
            binary = binary.resolve(strict=True)
            if process_binary is not None:
                result["process_binary_sha256"] = require_linux_binary(process_binary)
                process_binary = process_binary.resolve(strict=True)
                result["scope"] += "; public stdin, download, and output processes"
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
            copy_payload(commands, root, user, binary, target, result["binary_sha256"])
            process_target = "/home/cli-compat/payload/process.test"
            if process_binary is not None:
                copy_payload(commands, root, user, process_binary, process_target, result["process_binary_sha256"], "process-")
            identity = "set -eu; test $(id -u) = 2000; test $(uname -m) = x86_64; test $(dpkg --print-architecture) = amd64; cat /etc/os-release; uname -a; id; /bin/bash --version; stat -f -c %T /home/cli-compat"
            observed = commands.run("ubuntu-identity", user + ["/bin/bash", "--noprofile", "--norc", "-c", identity], 20)[0].decode("utf-8")
            if not re.search(r"^ID=ubuntu$", observed, re.MULTILINE):
                raise RuntimeError("The imported userspace is not Ubuntu")
            result["identity"] = observed
            control = "printf 'wsl-control-out\\n'; printf 'wsl-control-err\\n' >&2; exit 7"
            stdout, stderr, _ = commands.run("exit-control", user + ["/bin/bash", "--noprofile", "--norc", "-c", control], 10, expected=7)
            if stdout != b"wsl-control-out\n" or stderr != b"wsl-control-err\n":
                raise RuntimeError("WSL stream or exit control changed")
            test = ["/usr/bin/timeout", "--signal=TERM", "--kill-after=5s", "60s", target,
                    "-test.run=^" + ROOT + "$", "-test.timeout=45s", "-test.v"]
            stdout, stderr, code = commands.run("pager", user + test, 70)
            require_test_pass(stdout, stderr, code)
            result["tests"] = sorted(TESTS)
            if process_binary is not None:
                run_process_suite(commands, user, process_target)
                result["process_tests"] = sorted(PROCESS_TESTS)
                if digest(process_binary) != result["process_binary_sha256"]:
                    raise RuntimeError("The host process test binary changed")
            if digest(binary) != result["binary_sha256"]:
                raise RuntimeError("The host test binary changed")
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
    parser.add_argument("--cleanup", action="store_true")
    args = parser.parse_args()
    if not args.cleanup and args.binary is None:
        parser.error("--binary is required for the pilot")
    for number in (signal.SIGINT, signal.SIGTERM):
        signal.signal(number, interrupted)
    try:
        code = pilot(args.binary, args.cleanup, args.process_binary)
    except Exception as error:
        print(f"WSL pager pilot failed: {error}", file=sys.stderr)
        code = 128 + STOP if STOP else 1
    return code


if __name__ == "__main__":
    raise SystemExit(main())
