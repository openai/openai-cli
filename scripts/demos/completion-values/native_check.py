#!/usr/bin/env python3
"""Check native completion buffers without executing the completed commands."""

import argparse
from contextlib import contextmanager
import errno
import fcntl
import hashlib
import json
import os
from pathlib import Path
import pty
import select
import shlex
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


PROMPT = b"COMPLETION_TEST> "
CAPTURE_KEY = b"\x18\x07"
TIMEOUT = 10


def quoted_cases(empty=False):
    cases = []
    for placement, prefix, value in (
        ("separated", "openai --format ", "" if empty else "y"),
        ("assigned", "openai --format=", "" if empty else "y"),
        ("whole-assignment", "openai ", "--format=" if empty else "--format=y"),
    ):
        for quote_name, quote in (("single", "'"), ("double", '"')):
            for closed in (False, True):
                line = prefix + quote + value + (quote if closed else "")
                completed = prefix + quote + (value if empty else value[:-1] + "yaml")
                # Closed forms define the expected argv after quote removal.
                # Exact open forms also permit completed, unclosed values.
                # Empty prefixes have multiple choices. Preserve the empty value.
                expected = [completed + quote + " ", completed + quote]
                if not closed:
                    expected.append(completed)
                kind = "empty-" if empty else ""
                name = f"format-quoted-{kind}{placement}-{quote_name}-{'closed' if closed else 'open'}"
                cases.append((name, line, expected))
    return cases


def literal_inner_quote_cases():
    cases = []
    for flag, command, value in (
        ("format", "openai ", "y"),
        ("purpose", "openai files list ", "u"),
    ):
        for quote_name, outer, inner in (("single", "'", '"'), ("double", '"', "'")):
            for closed in (False, True):
                argument = "--" + flag + "=" + inner + value + inner
                line = command + outer + argument + (outer if closed else "")
                # Inner quotes are literal data inside the outer argument.
                # Preserve those bytes; yaml/user_data would change the value.
                expected = [command + outer + argument + outer]
                if not closed:
                    expected.append(line)
                name = f"{flag}-quoted-literal-inner-{quote_name}-{'closed' if closed else 'open'}"
                cases.append((name, line, expected))
    return cases


# Expected buffers retain the shell's final space for a unique completion.
CASES = [
    ("format-separated", "openai --format y", ["openai --format yaml "]),
    ("format-assigned", "openai --format=y", ["openai --format=yaml "]),
    ("format-empty-assigned", "openai --format=", ["openai --format="]),
    ("format-common-prefix", "openai --format j", ["openai --format json"]),
    *quoted_cases(),
    *quoted_cases(empty=True),
    *literal_inner_quote_cases(),
    ("error-format", "openai --format-error y", ["openai --format-error yaml "]),
    ("purpose-upload", "openai files upload --purpose u", ["openai files upload --purpose user_data "]),
    ("purpose-assigned", "openai files upload --purpose=u", ["openai files upload --purpose=user_data "]),
    ("quoted-preceding-file", 'openai files upload "upload space.txt" --purpose u',
     ['openai files upload "upload space.txt" --purpose user_data ']),
    ("root-before-command", "openai --format json files upload --purpose u",
     ["openai --format json files upload --purpose user_data "]),
    ("root-after-command", "openai files upload --format y", ["openai files upload --format yaml "]),
    ("purpose-list-output", "openai files list --purpose batch_o", ["openai files list --purpose batch_output "]),
    ("command-alias", "openai audio:transcriptions create --format y",
     ["openai audio:transcriptions create --format yaml "]),
    ("boundary-colon-input", "openai responses create --input : --format y",
     ["openai responses create --input : --format yaml "]),
    ("boundary-colon-organization", "openai --organization : files upload --purpose u",
     ["openai --organization : files upload --purpose user_data "]),
    ("boundary-quoted-alias", "openai 'chat:completions' create --format y",
     ["openai 'chat:completions' create --format yaml "]),
    ("freeform-model", "openai responses create --model y", ["openai responses create --model y"]),
    ("literal-double-dash", "openai responses create -- --format=y", ["openai responses create -- --format=y"]),
    ("file-flag", "openai files upload --file fixture", ["openai files upload --file fixture.txt "]),
    ("at-file", "openai responses create --input @fixture", ["openai responses create --input @fixture.txt "]),
    ("at-file-protocol", "openai responses create --input @file://fixture",
     ["openai responses create --input @file://fixture.txt "]),
    ("directory-format-separated", "openai --format y", ["openai --format yaml "]),
    ("directory-format-assigned", "openai --format=y", ["openai --format=yaml "]),
    ("format-quoted-directory-whole-assignment-single-closed", "openai '--format=y'",
     ["openai '--format=yaml'", "openai '--format=yaml' "]),
    ("format-quoted-directory-whole-assignment-double-closed", 'openai "--format=y"',
     ['openai "--format=yaml"', 'openai "--format=yaml" ']),
    ("directory-format-ambiguous", "openai --format j", ["openai --format json"]),
    ("directory-file", "openai files upload --file y", ["openai files upload --file yaml/"]),
    ("directory-command-no-collision", "openai models li", ["openai models list "]),
    ("directory-command-collision", "openai models li", ["openai models list "]),
    ("directory-flag-collision", "openai --forma", ["openai --format"]),
    ("directory-flag-after-data", "openai --organization --format --forma", ["openai --organization --format --format"]),
    ("directory-command-after-data", "openai --organization --format models li", ["openai --organization --format models list "]),
    ("wrapper-caller-only-directory", "openai --format y", ["openai --format yaml "]),
    ("wrapper-backend-only-directory", "openai --format y", ["openai --format yaml "]),
]

CASE_DIRECTORIES = {
    "directory-format-separated": ("yaml",),
    "directory-format-assigned": ("yaml",),
    "format-quoted-directory-whole-assignment-single-closed": ("--format=yaml",),
    "format-quoted-directory-whole-assignment-double-closed": ("--format=yaml",),
    "directory-format-ambiguous": ("json",),
    "directory-file": ("yaml",),
    "directory-command-no-collision": ("yaml",),
    "directory-command-collision": ("list",),
    "directory-flag-collision": ("--format",),
    "directory-flag-after-data": ("--format",),
    "directory-command-after-data": ("list",),
    "wrapper-caller-only-directory": ("yaml",),
}

CASE_BACKEND_DIRECTORIES = {
    "wrapper-caller-only-directory": (),
    "wrapper-backend-only-directory": ("yaml",),
}

# Bash suppresses static values when an actual replacement names a directory.
# Zsh keeps the static enum suggestions declared in CASES.
SHELL_EXPECTATIONS = {
    "zsh": {
        # Existing Zsh adapters retain quotes on preceding command aliases.
        "boundary-quoted-alias": ["openai 'chat:completions' create --format y"],
    },
    "bash": {
        "directory-format-separated": ["openai --format y"],
        "directory-format-assigned": ["openai --format=y"],
        "format-quoted-directory-whole-assignment-single-closed": ["openai '--format=y'"],
        "format-quoted-directory-whole-assignment-double-closed": ['openai "--format=y"'],
        "directory-format-ambiguous": ["openai --format j"],
        "directory-command-collision": ["openai models list/"],
        "directory-command-after-data": ["openai --organization --format models list/"],
        "wrapper-caller-only-directory": ["openai --format y"],
    },
}

# Continue typing after Tab to detect a cursor left inside a closing quote.
# The new --help word must remain a separate argument in the captured buffer.
CONTINUATIONS = {}
for case_name, case_line, case_expected in list(CASES):
    if case_name.endswith("-closed"):
        continuation_name = case_name + "-continue"
        CONTINUATIONS[continuation_name] = " --help"
        CASES.append((continuation_name, case_line, [value + " --help" for value in case_expected]))
        if case_name in CASE_DIRECTORIES:
            CASE_DIRECTORIES[continuation_name] = CASE_DIRECTORIES[case_name]
        for shell_expectations in SHELL_EXPECTATIONS.values():
            if case_name in shell_expectations:
                shell_expectations[continuation_name] = [value + " --help" for value in shell_expectations[case_name]]


ARGV_WRAPPER = r'''#!/bin/sh
# Record the adapter's exact argument vector, then execute the immutable binary.
printf '%s\0' "$#" "$@" >> "$COMPLETION_TEST_ARGV"
printf '%s\0' "${OPENAI_CLI_COMPLETION_STATIC_VALUES+x}" "${OPENAI_CLI_COMPLETION_STATIC_VALUES-}" >> "$COMPLETION_TEST_MARKERS"
if [ -f "$COMPLETION_TEST_BACKEND_CWD_FILE" ]; then
  IFS= read -r completion_test_backend_cwd < "$COMPLETION_TEST_BACKEND_CWD_FILE" || exit 93
  cd "$completion_test_backend_cwd" || exit 94
  pwd -P > "$COMPLETION_TEST_BACKEND_CWD_ACTUAL" || exit 95
fi
exec "$COMPLETION_TEST_BINARY" "$@"
'''


def expected_for_shell(name, expected, shell):
    return SHELL_EXPECTATIONS.get(shell, {}).get(name, expected)


@contextmanager
def fixture_directories(directory, names):
    created = []
    try:
        for name in names:
            path = directory / name
            if path.parent != directory:
                raise ValueError("Fixture directories must be direct children of the owned workspace.")
            path.mkdir(mode=0o700)
            created.append(path)
        yield
    finally:
        # Only remove the empty directories this case successfully created.
        for path in reversed(created):
            path.rmdir()


@contextmanager
def backend_directory(root, name, control, actual):
    if name not in CASE_BACKEND_DIRECTORIES:
        yield None
        return
    with tempfile.TemporaryDirectory(prefix="backend-cwd-", dir=root) as directory:
        target = Path(directory)
        with fixture_directories(target, CASE_BACKEND_DIRECTORIES[name]):
            try:
                control.write_text(str(target) + "\n", encoding="utf-8")
                yield target
            finally:
                control.unlink(missing_ok=True)
                actual.unlink(missing_ok=True)


def compare_completion(name, expected, observed):
    quoted = name.startswith(("format-quoted-", "purpose-quoted-literal-inner-"))
    if not quoted and name not in {"at-file", "at-file-protocol"}:
        return observed in expected, {"comparison_mode": "exact"}
    # These fixtures contain literal words, quotes, and escapes only.
    # shlex removes their quoting without executing shell expressions.
    expected_argv = []
    for candidate in expected:
        try:
            arguments = shlex.split(candidate, posix=True)
        except ValueError:
            continue
        if arguments not in expected_argv:
            expected_argv.append(arguments)
    comparison = {"comparison_mode": "argv", "expected_argv": expected_argv}
    try:
        comparison["observed_argv"] = shlex.split(observed, posix=True)
    except ValueError as error:
        comparison["argv_parse_error"] = str(error)
        if quoted and name.endswith("-open"):
            comparison["comparison_mode"] = "exact-unclosed-quote"
            return observed in expected, comparison
        return False, comparison
    return comparison["observed_argv"] in expected_argv, comparison


def backend_arguments(data):
    if not data:
        return []
    fields = data.split(b"\0")
    if fields.pop() != b"":
        raise ValueError("Backend argument log ended within a record.")
    calls = []
    index = 0
    while index < len(fields):
        count = int(fields[index])
        index += 1
        if count < 0 or index + count > len(fields):
            raise ValueError("Backend argument log has an invalid argument count.")
        calls.append([argument.decode("utf-8", "strict") for argument in fields[index:index + count]])
        index += count
    return calls


def backend_markers(data):
    if not data:
        return []
    fields = data.split(b"\0")
    if fields.pop() != b"" or len(fields) % 2:
        raise ValueError("Backend marker log ended within a record.")
    values = []
    for index in range(0, len(fields), 2):
        present, value = fields[index:index + 2]
        if present not in (b"", b"x") or not present and value:
            raise ValueError("Backend marker log has an invalid presence field.")
        values.append(value.decode("utf-8", "strict") if present else None)
    return values


def terminate_group(group, number):
    if group > 0 and group != os.getpgrp():
        try:
            os.killpg(group, number)
        except ProcessLookupError:
            # The process group can exit before cleanup sends its signal.
            pass


def raise_cleanup_errors(errors):
    # Control exceptions must remain outside check_shell's ordinary error handler.
    control = next((error for error in errors if not isinstance(error, Exception)), None)
    if control is not None:
        secondary = list(getattr(control, "cleanup_errors", ()))
        secondary.extend(error for error in errors if error is not control)
        if secondary:
            message = "Completion cleanup: " + "; ".join(f"{type(error).__name__}: {error}" for error in secondary)
            if hasattr(control, "add_note"):
                control.add_note(message)
            try:
                # SystemExit does not display exception notes automatically.
                sys.stderr.write(message + "\n")
                sys.stderr.flush()
            except BaseException as diagnostic_error:
                secondary.append(diagnostic_error)
            control.cleanup_errors = tuple(secondary)
        raise control
    if len(errors) == 1:
        raise errors[0]
    message = "; ".join(f"{type(error).__name__}: {error}" for error in errors)
    raise RuntimeError(message) from errors[0]


def generate_adapter(binary, shell, environment, destination):
    process = subprocess.Popen([str(binary), "@completion", shell], env=environment,
                               stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=TIMEOUT)
    except BaseException as error:
        failures = []
        # communicate can reap the child before reporting another failure.
        if process.returncode is None:
            try:
                terminate_group(process.pid, signal.SIGKILL)
            except BaseException as cleanup_error:
                failures.append(cleanup_error)
        deadline = time.monotonic() + TIMEOUT
        while True:
            try:
                process.communicate(timeout=max(0, deadline - time.monotonic()))
                break
            except BaseException as cleanup_error:
                failures.append(cleanup_error)
                interrupted = not isinstance(cleanup_error, Exception) or isinstance(cleanup_error, InterruptedError)
                if interrupted and process.returncode is None and time.monotonic() < deadline:
                    continue
                for stream in (process.stdout, process.stderr):
                    try:
                        stream.close()
                    except BaseException as close_error:
                        failures.append(close_error)
                break
        if failures:
            raise_cleanup_errors([error, *failures])
        raise
    destination.write_bytes(stdout)
    destination.with_suffix(".stderr").write_bytes(stderr)
    if process.returncode != 0 or stderr:
        raise RuntimeError(f"{shell} adapter generation failed: status={process.returncode}; stderr={stderr!r}")
    return hashlib.sha256(stdout).hexdigest()


class ShellSession:
    def __init__(self, shell, executable, environment, directory, transcript):
        self.status = None
        self.closed = False
        self.eof = False
        self.tail = bytearray()
        self.transcript = transcript.open("wb")
        arguments = [str(executable), "--noprofile", "--norc", "-i"]
        if shell == "zsh":
            arguments = [str(executable), "-f", "-i"]
        self.pid, self.terminal = pty.fork()
        if self.pid == 0:
            try:
                os.chdir(directory)
                os.execve(str(executable), arguments, environment)
            finally:
                # Successful exec never returns; failed setup must exit the child.
                os._exit(127)
        try:
            fcntl.ioctl(self.terminal, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
        except BaseException:
            self.close()
            raise

    def send(self, data):
        view = memoryview(data)
        deadline = time.monotonic() + TIMEOUT
        while view:
            if time.monotonic() >= deadline:
                raise TimeoutError("shell input exceeded its deadline")
            if select.select([], [self.terminal], [], 0.1)[1]:
                count = os.write(self.terminal, view)
                if count <= 0:
                    raise RuntimeError("shell stopped accepting input")
                view = view[count:]

    def pump(self, timeout=0.05):
        if not self.eof and select.select([self.terminal], [], [], timeout)[0]:
            try:
                data = os.read(self.terminal, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                self.transcript.write(data)
                self.transcript.flush()
                self.tail.extend(data)
                del self.tail[:-65536]
            else:
                self.eof = True
        # Keep the session leader unreaped until close finishes group signaling.

    def wait_for(self, predicate):
        deadline = time.monotonic() + TIMEOUT
        while not predicate():
            if self.eof:
                raise RuntimeError("shell terminal reached EOF before the expected event")
            if self.status is not None or time.monotonic() >= deadline:
                raise TimeoutError(f"shell event timed out; child status={self.status}")
            self.pump()

    def capture(self, line, capture_path, index, continuation=""):
        # Bash 3.2 accepts only a prefixed comment. Other widgets clear BUFFER.
        # Restrict the comment capture contract to these single-line fixtures.
        if "\n" in line + continuation or "\r" in line + continuation:
            raise ValueError("Native capture requires a single-line fixture.")
        self.send(line.encode("utf-8") + b"\t" + continuation.encode("utf-8") + CAPTURE_KEY)

        def complete():
            return capture_path.exists() and capture_path.read_bytes().count(b"\0") > index

        self.wait_for(complete)
        return capture_path.read_bytes().split(b"\0")[index].decode("utf-8", "strict")

    def close(self):
        if self.closed:
            return
        self.closed = True
        prior_error = sys.exc_info()[1]
        failures = []
        groups = [self.pid]

        def signal_groups(number):
            for group in groups:
                try:
                    if group != self.pid:
                        # Only the direct child's PID remains reserved for us.
                        # Recheck distinct groups; these checks are not atomic.
                        if os.getsid(group) != self.pid or os.getpgid(group) != group:
                            raise RuntimeError("foreground process-group ownership is uncertain")
                    terminate_group(group, number)
                except BaseException as error:
                    failures.append(error)

        try:
            # A previously reaped child cannot anchor further group signals.
            if self.status is None:
                try:
                    foreground = os.tcgetpgrp(self.terminal)
                    if foreground != self.pid:
                        if foreground <= 0:
                            raise RuntimeError("foreground process-group ownership is uncertain")
                        groups.insert(0, foreground)
                except BaseException as error:
                    failures.append(error)
                try:
                    signal_groups(signal.SIGTERM)
                    deadline = time.monotonic() + 2
                    while not self.eof and time.monotonic() < deadline:
                        self.pump()
                except BaseException as error:
                    failures.append(error)
                finally:
                    # Kill surviving descendants before releasing the PID anchor.
                    signal_groups(signal.SIGKILL)
                deadline = time.monotonic() + 2
                while self.status is None and time.monotonic() < deadline:
                    try:
                        waited, status = os.waitpid(self.pid, os.WNOHANG)
                        if waited:
                            self.status = os.waitstatus_to_exitcode(status)
                        else:
                            time.sleep(0.01)
                    except BaseException as error:
                        failures.append(error)
                        if not isinstance(error, Exception) or isinstance(error, InterruptedError):
                            continue
                        break
                if self.status is None:
                    failures.append(RuntimeError("shell did not exit within the cleanup deadline"))
        finally:
            try:
                os.close(self.terminal)
            except BaseException as error:
                failures.append(error)
            try:
                self.transcript.close()
            except BaseException as error:
                failures.append(error)
        if failures:
            errors = ([prior_error] if prior_error is not None else []) + failures
            raise_cleanup_errors(errors)


def startup(shell):
    common = """
PS1='COMPLETION_TEST> '
PS2='COMPLETION_MORE> '
"""
    if shell == "bash":
        return common + r'''
set -o emacs
bind 'set bell-style none'
bind 'set show-all-if-ambiguous off'
bind '"\C-i": complete'
source "$COMPLETION_TEST_ADAPTER" || exit 91
if (( BASH_VERSINFO[0] < 4 )); then
  # Bash 3.2 bind -x does not expose READLINE_LINE or READLINE_POINT.
  set +H
  set -o history
  shopt -s interactive_comments
  HISTCONTROL=''
  HISTIGNORE=''
  HISTSIZE=1000
  unset HISTTIMEFORMAT
  completion_test_capture_history() {
    local entry last='' marker='# __completion_capture__ '
    builtin history -w "$COMPLETION_TEST_HISTORY" || return
    while IFS= read -r entry; do last=$entry; done < "$COMPLETION_TEST_HISTORY"
    case "$last" in
      "$marker"*)
        printf '%s\0' "${last#"$marker"}" >> "$COMPLETION_TEST_CAPTURE" || return
        # Clear owned history to prevent duplicate capture on another prompt.
        builtin history -c
        ;;
    esac
  }
  PROMPT_COMMAND=completion_test_capture_history
  # Prepend the comment before accepting. Quotes and substitutions stay inert.
  bind '"\C-x\C-g": "\C-a# __completion_capture__ \C-e\C-m"'
  printf 'bash-history-comment\n' > "$COMPLETION_TEST_CAPTURE_MODE"
else
  completion_test_capture() {
    printf '%s\0' "$READLINE_LINE" >> "$COMPLETION_TEST_CAPTURE"
    READLINE_LINE=''
    READLINE_POINT=0
  }
  bind -x '"\C-x\C-g": completion_test_capture'
  printf 'bash-readline-buffer\n' > "$COMPLETION_TEST_CAPTURE_MODE"
fi
printf '%s\n' "$BASH_VERSION" > "$COMPLETION_TEST_VERSION"
printf 'ready\n' > "$COMPLETION_TEST_READY"
'''
    return common + r'''
autoload -Uz compinit
compinit -D || exit 92
setopt NO_BEEP
unsetopt MENU_COMPLETE AUTO_MENU
bindkey -e
bindkey '^I' expand-or-complete
source "$COMPLETION_TEST_ADAPTER" || exit 91
completion_test_capture() {
  printf '%s\0' "$BUFFER" >> "$COMPLETION_TEST_CAPTURE"
  BUFFER=''
  CURSOR=0
  zle reset-prompt
}
zle -N completion_test_capture
bindkey '^X^G' completion_test_capture
printf 'zsh-buffer\n' > "$COMPLETION_TEST_CAPTURE_MODE"
printf '%s\n' "$ZSH_VERSION" > "$COMPLETION_TEST_VERSION"
printf 'ready\n' > "$COMPLETION_TEST_READY"
'''


def report_case(report, name, line, expected, status, observed=None, detail=None, backend_argv=None, comparison=None, markers=None):
    expected = expected_for_shell(name, expected, report["shell"])
    result = dict(case=name, shell=report["shell"], executable=report["executable"],
                  input=line, expected=expected, observed=observed, status=status)
    if name in CASE_DIRECTORIES:
        result["fixture_directories"] = list(CASE_DIRECTORIES[name])
    if name in CASE_BACKEND_DIRECTORIES:
        result["backend_fixture_directories"] = list(CASE_BACKEND_DIRECTORIES[name])
    if name in CONTINUATIONS:
        result["after_tab"] = CONTINUATIONS[name]
    if detail:
        result["detail"] = detail
    if backend_argv is not None:
        result["backend_argv"] = backend_argv
    if comparison is not None:
        result.update(comparison)
    if markers is not None:
        result["static_values_markers"] = markers
        if (status == "pass" and report["shell"] == "bash"
                and name.startswith("format-quoted-empty-") and "-closed" in name
                and markers and all(value == "0" for value in markers)):
            result["suggestion_status"] = "preserved-without-suggestions"
    report["cases"].append(result)
    print(json.dumps(result), flush=True)


def check_shell(binary, shell, executable, output):
    report = dict(shell=shell, executable=str(executable), cases=[])
    if not executable.is_file() or not os.access(executable, os.X_OK):
        for name, line, expected in CASES:
            report_case(report, name, line, expected, "not-run", detail="Native shell is unavailable.")
        return report
    session = None
    next_case = 0
    try:
        with tempfile.TemporaryDirectory(prefix="completion-values-") as directory:
            root = Path(directory)
            home, work, bindir = root / "home", root / "work", root / "bin"
            for path in (home, work, bindir):
                path.mkdir(mode=0o700)
            wrapper = bindir / "openai"
            wrapper.write_text(ARGV_WRAPPER, encoding="utf-8")
            wrapper.chmod(0o700)
            (output / f"{shell}-argv-wrapper.sh").write_text(ARGV_WRAPPER, encoding="utf-8")
            (work / "fixture.txt").write_text("synthetic completion fixture\n", encoding="utf-8")
            adapter = output / f"{shell}-adapter.sh"
            argument_log = output / f"{shell}-backend-argv.nul"
            argument_log.touch(mode=0o600)
            report["backend_argument_log"] = str(argument_log)
            marker_log = output / f"{shell}-static-values-markers.nul"
            marker_log.touch(mode=0o600)
            report["backend_marker_log"] = str(marker_log)
            capture = root / "captured-buffers"
            capture_mode = root / "capture-mode"
            backend_control, backend_actual = root / "backend-cwd-control", root / "backend-cwd-actual"
            ready, version = root / "ready", root / "version"
            environment = {
                "HOME": str(home), "ZDOTDIR": str(home), "XDG_CONFIG_HOME": str(home),
                "PATH": str(bindir) + ":/usr/bin:/bin", "SHELL": str(executable),
                "TERM": "xterm-256color", "LC_ALL": "C", "INPUTRC": "/dev/null",
                "GOMAXPROCS": "2", "BASH_SILENCE_DEPRECATION_WARNING": "1",
                "OPENAI_BASE_URL": "invalid-completion-url",
                "OPENAI_CUSTOM_HEADERS": "invalid-completion-headers",
                "COMPLETION_TEST_ADAPTER": str(adapter), "COMPLETION_TEST_CAPTURE": str(capture),
                "COMPLETION_TEST_READY": str(ready), "COMPLETION_TEST_VERSION": str(version),
                "COMPLETION_TEST_ARGV": str(argument_log), "COMPLETION_TEST_BINARY": str(binary),
                "COMPLETION_TEST_MARKERS": str(marker_log),
                "COMPLETION_TEST_HISTORY": str(root / "capture-history"),
                "COMPLETION_TEST_CAPTURE_MODE": str(capture_mode),
                "COMPLETION_TEST_BACKEND_CWD_FILE": str(backend_control),
                "COMPLETION_TEST_BACKEND_CWD_ACTUAL": str(backend_actual),
            }
            report["adapter_sha256"] = generate_adapter(binary, shell, environment, adapter)
            script = root / "startup.sh"
            script.write_text(startup(shell), encoding="utf-8")
            (output / f"{shell}-startup.sh").write_text(startup(shell), encoding="utf-8")
            transcript = output / f"{shell}.transcript"
            report["transcript"] = str(transcript)
            try:
                session = ShellSession(shell, executable, environment, work, transcript)
                session.send(("source " + shlex.quote(str(script)) + "\n").encode("utf-8"))
                session.wait_for(lambda: ready.exists() and PROMPT in session.tail)
                report["version"] = version.read_text(encoding="utf-8").strip()
                report["capture_mode"] = capture_mode.read_text(encoding="utf-8").strip()
                for index, (name, line, expected) in enumerate(CASES):
                    expected = expected_for_shell(name, expected, shell)
                    argument_offset = argument_log.stat().st_size
                    marker_offset = marker_log.stat().st_size
                    observed_backend = None
                    with backend_directory(root, name, backend_control, backend_actual) as target:
                        with fixture_directories(work, CASE_DIRECTORIES.get(name, ())):
                            observed = session.capture(line, capture, index, CONTINUATIONS.get(name, ""))
                            calls = backend_arguments(argument_log.read_bytes()[argument_offset:])
                            markers = backend_markers(marker_log.read_bytes()[marker_offset:])
                            if target is not None:
                                observed_backend = backend_actual.read_text(encoding="utf-8").rstrip("\n")
                                if Path(observed_backend) != target.resolve():
                                    raise RuntimeError("The wrapper did not enter the requested backend directory.")
                    matches, comparison = compare_completion(name, expected, observed)
                    if observed_backend is not None:
                        comparison["caller_working_directory"] = str(work.resolve())
                        comparison["backend_working_directory"] = observed_backend
                    status = "pass" if matches else "fail"
                    detail = None
                    if not calls:
                        status = "error"
                        detail = "The shell did not invoke the completion backend."
                    elif any(not call or call[0] != "__complete" for call in calls):
                        status = "error"
                        detail = "The shell invoked the binary outside the completion backend."
                    elif len(markers) != len(calls):
                        status = "error"
                        detail = "Backend marker and argument logs have different call counts."
                    elif name == "format-empty-assigned" and any(value != "1" for value in markers):
                        status = "error"
                        detail = "The current adapter did not enable static suggestions for the unquoted empty assignment."
                    report_case(report, name, line, expected, status, observed, detail, calls, comparison, markers)
                    next_case = index + 1
            finally:
                if session is not None:
                    session.close()
                    report["cleanup_status"] = session.status
    except Exception as error:
        report["error"] = f"{type(error).__name__}: {error}"
        for index, (name, line, expected) in enumerate(CASES[next_case:], next_case):
            status = "error" if index == next_case else "not-run"
            report_case(report, name, line, expected, status, detail=report["error"])
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path, help="Absolute path to an immutable openai binary.")
    parser.add_argument("output", type=Path, help="Absolute path to an empty evidence directory.")
    parser.add_argument("--bash", type=Path, default=Path("/bin/bash"))
    parser.add_argument("--zsh", type=Path, default=Path("/bin/zsh"))
    args = parser.parse_args()
    if any(not path.is_absolute() for path in (args.binary, args.output, args.bash, args.zsh)):
        parser.error("Binary, output, and shell paths must be absolute.")
    if not args.binary.is_file() or not os.access(args.binary, os.X_OK):
        parser.error("Binary must be an executable file.")
    args.output.mkdir(parents=True, exist_ok=True)
    if any(args.output.iterdir()):
        parser.error("Output directory must be empty.")

    def interrupt(number, _frame):
        raise SystemExit(128 + number)

    for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(number, interrupt)
    report = dict(binary=str(args.binary), binary_sha256=hashlib.sha256(args.binary.read_bytes()).hexdigest(), shells=[])
    try:
        for shell, executable in (("bash", args.bash), ("zsh", args.zsh)):
            report["shells"].append(check_shell(args.binary, shell, executable, args.output))
    finally:
        (args.output / "report.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    cases = [case for shell in report["shells"] for case in shell["cases"]]
    failed = (any(shell.get("error") for shell in report["shells"])
              or any(case["status"] not in {"pass", "not-run"} for case in cases))
    incomplete = any(case["status"] == "not-run" for case in cases)
    print(f"Native cases: {sum(case['status'] == 'pass' for case in cases)}/{len(cases)} passed.")
    return 1 if failed else 2 if incomplete else 0


if __name__ == "__main__":
    sys.exit(main())
