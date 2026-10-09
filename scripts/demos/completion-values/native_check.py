#!/usr/bin/env python3
"""Check native completion buffers without executing the completed commands."""

import argparse
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
    ("freeform-model", "openai responses create --model y", ["openai responses create --model y"]),
    ("literal-double-dash", "openai responses create -- --format=y", ["openai responses create -- --format=y"]),
    ("file-flag", "openai files upload --file fixture", ["openai files upload --file fixture.txt "]),
    ("at-file", "openai responses create --input @fixture", ["openai responses create --input @fixture.txt "]),
    ("at-file-protocol", "openai responses create --input @file://fixture",
     ["openai responses create --input @file://fixture.txt "]),
]

# Continue typing after Tab to detect a cursor left inside a closing quote.
# The new --help word must remain a separate argument in the captured buffer.
CONTINUATIONS = {}
for case_name, case_line, case_expected in list(CASES):
    if case_name.endswith("-closed"):
        continuation_name = case_name + "-continue"
        CONTINUATIONS[continuation_name] = " --help"
        CASES.append((continuation_name, case_line, [value + " --help" for value in case_expected]))


ARGV_WRAPPER = r'''#!/bin/sh
# Record the adapter's exact argument vector, then execute the immutable binary.
printf '%s\0' "$#" "$@" >> "$COMPLETION_TEST_ARGV"
exec "$COMPLETION_TEST_BINARY" "$@"
'''


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


def terminate_group(group, number):
    if group > 0 and group != os.getpgrp():
        try:
            os.killpg(group, number)
        except ProcessLookupError:
            pass


def generate_adapter(binary, shell, environment, destination):
    process = subprocess.Popen([str(binary), "@completion", shell], env=environment,
                               stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, start_new_session=True)
    try:
        stdout, stderr = process.communicate(timeout=TIMEOUT)
    except BaseException:
        terminate_group(process.pid, signal.SIGKILL)
        process.communicate(timeout=TIMEOUT)
        raise
    finally:
        # Reap descendants even if their leader already exited.
        terminate_group(process.pid, signal.SIGKILL)
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
            except BaseException:
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
        if self.status is None:
            waited, status = os.waitpid(self.pid, os.WNOHANG)
            if waited:
                self.status = os.waitstatus_to_exitcode(status)

    def wait_for(self, predicate):
        deadline = time.monotonic() + TIMEOUT
        while not predicate():
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
        try:
            # Completion can run in the terminal's foreground process group.
            try:
                foreground = os.tcgetpgrp(self.terminal)
            except OSError:
                foreground = self.pid
            for number in (signal.SIGTERM, signal.SIGKILL):
                terminate_group(foreground, number)
                terminate_group(self.pid, number)
                deadline = time.monotonic() + 2
                while self.status is None and time.monotonic() < deadline:
                    self.pump()
                if self.status is not None:
                    # Also remove descendants after the session leader exits.
                    terminate_group(foreground, signal.SIGKILL)
                    terminate_group(self.pid, signal.SIGKILL)
                    break
            if self.status is None:
                raise RuntimeError("shell did not exit within the cleanup deadline")
        finally:
            os.close(self.terminal)
            self.transcript.close()


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


def report_case(report, name, line, expected, status, observed=None, detail=None, backend_argv=None, comparison=None):
    result = dict(case=name, shell=report["shell"], executable=report["executable"],
                  input=line, expected=expected, observed=observed, status=status)
    if name in CONTINUATIONS:
        result["after_tab"] = CONTINUATIONS[name]
    if detail:
        result["detail"] = detail
    if backend_argv is not None:
        result["backend_argv"] = backend_argv
    if comparison is not None:
        result.update(comparison)
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
            capture = root / "captured-buffers"
            capture_mode = root / "capture-mode"
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
                "COMPLETION_TEST_HISTORY": str(root / "capture-history"),
                "COMPLETION_TEST_CAPTURE_MODE": str(capture_mode),
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
                    argument_offset = argument_log.stat().st_size
                    observed = session.capture(line, capture, index, CONTINUATIONS.get(name, ""))
                    calls = backend_arguments(argument_log.read_bytes()[argument_offset:])
                    matches, comparison = compare_completion(name, expected, observed)
                    status = "pass" if matches else "fail"
                    detail = None
                    if not calls:
                        status = "error"
                        detail = "The shell did not invoke the completion backend."
                    elif any(not call or call[0] != "__complete" for call in calls):
                        status = "error"
                        detail = "The shell invoked the binary outside the completion backend."
                    report_case(report, name, line, expected, status, observed, detail, calls, comparison)
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
