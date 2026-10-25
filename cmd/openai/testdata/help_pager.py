"""Exercise the production entrypoint in isolated POSIX terminals. No API calls."""

import errno
import fcntl
import os
import pty
import resource
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time


def check(name, args=(), extra=None, redirected=None, action=b"q", code=0, width=100, cancel_context=False, startup_failure=False):
    with tempfile.TemporaryDirectory(prefix="help-pager-") as home:
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, width, 0, 0))
        original = termios.tcgetattr(slave)
        # Compare public mutable flags. An unchanged Go --version control also
        # adds macOS's private 0x10000 flag before any viewer runs.
        status_flags = os.O_NONBLOCK | os.O_APPEND | os.O_ASYNC
        original_flags = fcntl.fcntl(slave, fcntl.F_GETFL) & status_flags
        env = {
            "PATH": "/usr/bin:/bin", "HOME": home, "USERPROFILE": home,
            "XDG_CONFIG_HOME": home, "XDG_CACHE_HOME": home,
            "TERM": "xterm-256color", "LANG": "en_US.UTF-8",
            "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1", "OPENAI_CLI_NO_UPDATE_CHECK": "1",
            "OPENAI_BASE_URL": "not%url", "OPENAI_API_KEY": "", "OPENAI_ADMIN_KEY": "",
            "OPENAI_MTLS_CLIENT_CERT_FILE": "/synthetic/missing-cert.pem",
            "OPENAI_MTLS_CLIENT_KEY_FILE": "/synthetic/missing-key.pem",
        }
        env.update(extra or {})
        streams = {stream: slave for stream in ("stdin", "stdout", "stderr")}
        if redirected:
            streams[redirected] = subprocess.DEVNULL
        control = os.pipe() if cancel_context else ()
        process_test = "TestMainDispatchProcess"
        if cancel_context:
            env["OPENAI_CLI_HELP_PAGER_CONTEXT_FD"] = str(control[0])
            process_test = "TestMainHelpPagerContextProcess"
        if startup_failure:
            env["OPENAI_CLI_HELP_PAGER_EXHAUST_FDS"] = "1"
            process_test = "TestMainHelpPagerStartupFailureProcess"
        command = [sys.argv[1], f"-test.run=^{process_test}$", "--", "openai", *args]
        limit = (lambda: resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))) if startup_failure else None
        child = subprocess.Popen(command, env=env, cwd=home, pass_fds=control[:1], preexec_fn=limit, **streams)
        data = bytearray()
        deadline = time.monotonic() + 10

        def read_until(marker=None, start=0):
            while True:
                if marker and marker in data[start:]:
                    return
                if time.monotonic() > deadline:
                    raise TimeoutError(f"{name}: missing {marker!r}; output={bytes(data)!r}")
                ready, _, _ = select.select([master], [], [], .05)
                if ready:
                    try:
                        chunk = os.read(master, 65536)
                    except OSError as error:
                        if error.errno != errno.EIO:
                            raise
                        chunk = b""
                    if chunk:
                        data.extend(chunk)
                        continue
                if child.poll() is not None:
                    if marker:
                        raise AssertionError(f"{name}: exited before {marker!r}: {bytes(data)!r}")
                    return

        try:
            if action is not None:
                read_until(b"q: quit")
                assert b"\x1b[?1049h" in data, (name, "no alternate screen")
                assert b"What are we making today?" in data, (name, "banner not visible at startup")
                assert b"Use the OpenAI API" in data, (name, "missing purpose below banner")
                assert b"Help needs no API key" not in data, (name, "started at bottom")
                if name == "navigation":
                    start = len(data)
                    os.write(master, b"G")
                    read_until(b"Help needs no API key", start)
                    start = len(data)
                    os.write(master, b"g")
                    read_until(b"What are we making today?", start)
                    start = len(data)
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 12, 40, 0, 0))
                    child.send_signal(signal.SIGWINCH)
                    read_until(b"q: quit", start)
                if cancel_context:
                    os.write(control[1], b"x")
                elif action == "signal":
                    child.send_signal(signal.SIGINT)
                elif action == "terminate":
                    child.send_signal(signal.SIGTERM)
                else:
                    os.write(master, action)
            read_until()
            assert child.wait(timeout=2) == code, (name, child.returncode, bytes(data))
            restored = termios.tcgetattr(slave)
            # macOS sets PENDIN when tcsetattr restores canonical input, even in
            # an independent raw/restore control. It is pending work, not a mode.
            if sys.platform == "darwin":
                restored[3] &= ~termios.PENDIN
                original[3] &= ~termios.PENDIN
            assert restored == original, (name, "terminal mode was not restored", original, restored)
            restored_flags = fcntl.fcntl(slave, fcntl.F_GETFL) & status_flags
            assert restored_flags == original_flags, (name, "descriptor flags changed", original_flags, restored_flags)
            if action is not None:
                assert b"\x1b[?1049l" in data, (name, "alternate screen was not restored")
            else:
                assert b"\x1b[?1049h" not in data, (name, "unexpected pager")
                if startup_failure:
                    assert b"Could not display help" in data, (name, "missing safe startup failure", bytes(data))
                    assert b"openai --help" in data, (name, "missing plain-help recovery")
                elif redirected != "stdout":
                    assert b"GLOBAL FLAGS" in data, (name, "missing complete help")
            if extra and "NO_COLOR" in extra:
                assert b"38;" not in data and b"38:" not in data, (name, "color opt-out ignored")
            assert not os.listdir(home), (name, "help changed state")
            print(f"PASS {name}")
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            os.close(master)
            os.close(slave)
            for fd in control:
                os.close(fd)


check("navigation")
check("no-color", extra={"NO_COLOR": "1"}, width=40)
check("escape", action=b"\x1b")
check("ctrl-c", action=b"\x03", code=130)
check("interrupt", action="signal", code=130)
check("terminate", action="terminate", code=143)
check("context-cancel", cancel_context=True, code=1)
check("startup-failure", startup_failure=True, action=None, code=1)
for name, args in (("explicit-help", ("--help",)), ("help-command", ("help",)),
                   ("nested-help", ("models", "--help")), ("explicit-format", ("--format=json",))):
    check(name, args, action=None)
for stream in ("stdin", "stdout", "stderr"):
    check("redirected-" + stream, redirected=stream, action=None)
check("ci", extra={"CI": "true"}, action=None)
check("dumb", extra={"TERM": "dumb"}, action=None)
