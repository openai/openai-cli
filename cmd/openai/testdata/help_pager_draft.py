"""Keep a real zsh in control of submitting a typed help draft."""
import errno
import fcntl
import os
from pathlib import Path
import pty
import select
import shlex
import signal
import struct
import sys
import tempfile
import termios
import time

binary, zsh = sys.argv[1:]
prompt = b"draft-test % "
cursor = b"\x1b[6n"
position = b"\x1b[1;1R"
paste_begin = b"\x1b[200~"
paste_end = b"\x1b[201~"


def check(kind):
    with tempfile.TemporaryDirectory(prefix="help-draft-") as tmp:
        home = Path(tmp)
        launcher = home / "openai"
        launcher.write_text("#!/bin/sh\nexec " + shlex.quote(binary) +
                            ' -test.run=^TestMainDispatchProcess$ -- openai "$@"\n')
        launcher.chmod(0o700)
        (home / ".zshrc").write_text("""
PS1='draft-test %% '
unsetopt prompt_cr
source <(openai @completion zsh --suggestions --suggestions-only)
record_buffer() { builtin print -rn -- "$BUFFER" > "$HOME/recorded"; BUFFER=''; : > "$HOME/ready"; }
zle -N record_buffer
bindkey '^]' record_buffer
preexec() { builtin print -r -- "$1" >> "$HOME/submitted"; }
""")
        pid, fd = pty.fork()
        if not pid:
            fcntl.ioctl(0, termios.TIOCSWINSZ, struct.pack("HHHH", 34, 120, 0, 0))
            os.execve(zsh, [zsh, "-di"], dict(PATH=tmp + ":/usr/bin:/bin", HOME=tmp, ZDOTDIR=tmp,
                        TERM="xterm-256color", TERM_PROGRAM="Apple_Terminal", LANG="en_US.UTF-8",
                        OPENAI_CLI_MAIN_DISPATCH_PROCESS="1", OPENAI_CLI_NO_UPDATE_CHECK="1",
                        OPENAI_CLI_NO_AUTO_UPDATE="1", OPENAI_BASE_URL="http://127.0.0.1:1",
                        HISTFILE="/dev/null"))
        raw = bytearray()
        answered_cursor = 0
        auto_cursor = True

        def read_until(predicate):
            nonlocal answered_cursor
            deadline = time.monotonic() + 5
            while not predicate():
                if time.monotonic() >= deadline:
                    raise AssertionError(f"{kind}: expected terminal state did not arrive")
                if select.select([fd], [], [], .025)[0]:
                    raw.extend(os.read(fd, 65536))
                while auto_cursor and answered_cursor < raw.count(cursor):
                    if kind == "split-admission" and answered_cursor == 0:
                        os.write(fd, position[:1])
                        time.sleep(.4)
                        os.write(fd, position[1:])
                    else:
                        os.write(fd, position)
                    answered_cursor += 1
        try:
            read_until(lambda: prompt in raw)
            os.write(fd, b"openai\r")
            read_until(lambda: b"Type a command" in raw)
            assert b"START HERE" in raw
            auto_cursor = False
            queries = raw.count(cursor)
            if kind == "type":
                os.write(fd, b"git status ")
                read_until(lambda: raw.count(cursor) > queries)
                # The suffix belongs to the shell even if it shares the reply's write.
                os.write(fd, b"-s" + position + " café".encode())
                expected = "git status -s café".encode()
            elif kind == "first-paste":
                # Escape alone could also mean the user wants to quit.
                os.write(fd, paste_begin[:1])
                time.sleep(.4)
                expected = b"first line\nsecond line"
                os.write(fd, paste_begin[1:] + expected + paste_end)
                read_until(lambda: raw.count(cursor) >= queries + 2)
                os.write(fd, position * 2)
            elif kind == "paste":
                os.write(fd, b":")
                read_until(lambda: raw.count(cursor) > queries)
                expected = b":one\nsecond line\n"
                os.write(fd, paste_begin[:3])
                time.sleep(.4)  # Longer than the missing-reply idle fallback.
                os.write(fd, paste_begin[3:] + expected[1:] + paste_end + position)
            else:
                os.write(fd, b"q")
                read_until(lambda: raw.count(cursor) > queries)
                if kind == "quit-then-type":
                    expected = b"git status --short"
                    # A slow reply is one terminal event. Keep both neighboring words.
                    os.write(fd, b"git status" + position[:3])
                    time.sleep(.4)
                    os.write(fd, position[3:] + b" --short")
                else:
                    expected = b""
                    os.write(fd, position)
            read_until(lambda: raw.count(prompt) >= 2)
            assert b"Help needs no API key or internet." in raw
            os.write(fd, b"\x1d")
            record = home / "recorded"
            read_until((home / "ready").exists)
            assert record.read_bytes() == expected, f"{kind}: changed editable draft"
            assert (home / "submitted").read_text().splitlines() == ["openai"], f"{kind}: draft was submitted"
            os.write(fd, b"exit 0\r")
            deadline = time.monotonic() + 5
            while time.monotonic() < deadline:
                ended, rc = os.waitpid(pid, os.WNOHANG)
                if ended:
                    pid = 0
                    assert os.waitstatus_to_exitcode(rc) == 0
                    break
                if select.select([fd], [], [], .05)[0]:
                    try:
                        os.read(fd, 65536)
                    except OSError as error:
                        if error.errno != errno.EIO:
                            raise
            else:
                raise AssertionError("the original shell did not exit")
        finally:
            os.close(fd)
            if pid:
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os.waitpid(pid, 0)


for case in ("type", "paste", "first-paste", "quit", "quit-then-type", "split-admission"):
    check(case)
