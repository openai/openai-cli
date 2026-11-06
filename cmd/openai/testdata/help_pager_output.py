"""A stopped terminal must release native modes without draining its output."""
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


def saturate(slave, fill):
    count = 0
    while count < 1 << 20:
        try:
            count += os.write(fill, b" " * 4096)
        except BlockingIOError:
            break
    assert 0 < count < 1 << 20, "PTY did not saturate"
    termios.tcflow(slave, termios.TCOOFF)


def check(binary, action, expected):
    with tempfile.TemporaryDirectory() as home:
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 0, 0))
        before = termios.tcgetattr(slave)
        mask = os.O_NONBLOCK | os.O_APPEND | os.O_ASYNC
        flags = fcntl.fcntl(slave, fcntl.F_GETFL) & mask
        fill = os.open(os.ttyname(slave), os.O_WRONLY | os.O_NONBLOCK | os.O_NOCTTY)
        env = dict(HOME=home, PATH="/usr/bin:/bin", TERM="xterm-256color", NO_COLOR="1",
                   OPENAI_CLI_BARE_INPUT="1", OPENAI_CLI_MAIN_DISPATCH_PROCESS="1",
                   OPENAI_CLI_NO_UPDATE_CHECK="1", OPENAI_CLI_NO_AUTO_UPDATE="1",
                   OPENAI_BASE_URL="http://127.0.0.1:1")
        control = os.pipe() if action == "context" else ()
        entrypoint = "TestMainDispatchProcess"
        if control:
            env["OPENAI_CLI_HELP_PAGER_CONTEXT_FD"] = str(control[0])
            entrypoint = "TestMainHelpPagerContextProcess"
        if action == "startup":
            saturate(slave, fill)
        proc = subprocess.Popen([binary, f"-test.run=^{entrypoint}$", "--", "openai"], cwd=home,
                                env=env, stdin=slave, stdout=slave, stderr=slave, pass_fds=control[:1])
        try:
            deadline = time.monotonic() + 5
            if action == "startup":
                # Cancellation can precede the first frame. Raw mode proves ownership.
                while termios.tcgetattr(slave)[3] & termios.ICANON:
                    assert time.monotonic() < deadline, "pager did not enter raw mode"
                    time.sleep(.001)
            else:
                data = bytearray()
                while b"q: end" not in data:
                    assert time.monotonic() < deadline, (action, "first page did not appear")
                    if select.select([master], [], [], .05)[0]:
                        data.extend(os.read(master, 65536))
                saturate(slave, fill)
                os.write(master, b"\x1b[B")  # Draw while the terminal cannot accept more output.
            if action == "key":
                os.write(master, b"\x03")
            elif control:
                os.write(control[1], b"x")
            else:
                proc.send_signal(signal.SIGTERM if action == "startup" else action)
            assert proc.wait(timeout=3) == expected, (action, proc.returncode)
            restored = termios.tcgetattr(slave)
            if sys.platform == "darwin":
                before[3] &= ~termios.PENDIN
                restored[3] &= ~termios.PENDIN
            assert restored == before, (action, "terminal mode changed")
            assert fcntl.fcntl(slave, fcntl.F_GETFL) & mask == flags, (action, "descriptor flags changed")
            print(f"PASS {action}")
        finally:
            termios.tcflow(slave, termios.TCOON)
            if proc.poll() is None:
                proc.kill()
                proc.wait()
            for fd in (*control, fill, slave, master):
                os.close(fd)


for action, code in ((signal.SIGTERM, 143), (signal.SIGINT, 130), ("key", 130), ("context", 1), ("startup", 143)):
    check(sys.argv[1], action, code)
