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


def answer_status_request(master, handoff, pending_command):
    if handoff == "no response":
        return
    reply = b"\x1b[0n"
    if handoff == "interrupt":
        reply = b"\x03" + reply
    if handoff in ("split", "large"):
        # An emulator can have generated wheel reports before it sees the reset.
        report = b"\x1b[<65;75;28M"
        os.write(master, report[:5])
        time.sleep(.016)
        backlog = memoryview(report[5:] + report * (10000 if handoff == "large" else 180))
        while backlog:
            backlog = backlog[os.write(master, backlog):]
        reply += pending_command
    os.write(master, reply)


def check(name, args=(), extra=None, redirected=None, action=b"q", code=0, width=100, height=24,
          cancel_context=False, startup_failure=False, compare_scrollback=False, launcher="openai", mouse_handoff=None):
    with tempfile.TemporaryDirectory(prefix="help-pager-") as home:
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
        if compare_scrollback:
            # Compare bytes written by the CLI. macOS PTY newline translation
            # can insert a second CR at an output-buffer boundary.
            settings = termios.tcgetattr(slave)
            settings[1] &= ~termios.ONLCR
            termios.tcsetattr(slave, termios.TCSANOW, settings)
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
        observed_signal = mouse_handoff == "reply after signal"
        control = os.pipe() if cancel_context or observed_signal else ()
        process_test = "TestMainDispatchProcess"
        if cancel_context:
            env["OPENAI_CLI_HELP_PAGER_CONTEXT_FD"] = str(control[0])
            process_test = "TestMainHelpPagerContextProcess"
        if observed_signal:
            env["OPENAI_CLI_HELP_PAGER_SIGNAL_FD"] = str(control[1])
            process_test = "TestMainHelpPagerObservedSignalProcess"
        if startup_failure:
            env["OPENAI_CLI_HELP_PAGER_EXHAUST_FDS"] = "1"
            process_test = "TestMainHelpPagerStartupFailureProcess"
        command = [sys.argv[1], f"-test.run=^{process_test}$", "--", launcher, *args]
        limit = (lambda: resource.setrlimit(resource.RLIMIT_NOFILE, (64, 64))) if startup_failure else None
        child_fds = control[1:] if observed_signal else control[:1]
        child = subprocess.Popen(command, env=env, cwd=home, pass_fds=child_fds, preexec_fn=limit, **streams)
        data = bytearray()
        answered = False
        pending_command = b"printf protected-typeahead\n"
        deadline = time.monotonic() + 10

        def read_until(marker=None, start=0):
            nonlocal answered, deadline
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
                        if not answered and b"\x1b[5n" in data:
                            answered = True
                            assert data.index(b"\x1b[?1000l") < data.index(b"\x1b[?1006l") < data.index(b"\x1b[5n")
                            if observed_signal or mouse_handoff == "signal without reply":
                                assert child.poll() is None and b"\x1b[?25h" not in data, "handoff already ended"
                                deadline = min(deadline, time.monotonic() + 3)
                                child.terminate()
                                if not observed_signal:
                                    continue  # Let the bounded missing-reply cleanup finish.
                                assert select.select([control[0]], [], [], 2)[0], "Go did not receive SIGTERM"
                                assert os.read(control[0], 1) == b"T"
                            answer_status_request(master, mouse_handoff, pending_command)
                        continue
                if child.poll() is not None:
                    if marker:
                        raise AssertionError(f"{name}: exited before {marker!r}: {bytes(data)!r}")
                    return

        try:
            if action is not None:
                read_until(b"q: end")
                assert b"\x1b[?1049h" not in data and b"\x1b[?1006h" in data, (name, "first page must enter native scrollback")
                assert b"What are we making today?" in data, (name, "banner not visible at startup")
                assert b"Use the OpenAI API" in data, (name, "missing purpose below banner")
                assert b"Help needs no API key" not in data, (name, "started at bottom")
                if name == "navigation":
                    start = len(data)
                    os.write(master, b" ")
                    read_until(b"LOCAL TOOLS", start)
                    start = len(data)
                    os.write(master, b"g")
                    read_until(b"What are we making today?", start)
                    start = len(data)
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 12, 40, 0, 0))
                    child.send_signal(signal.SIGWINCH)
                    read_until(b"q: end", start)
                if name == "resize to bottom":
                    os.write(master, b" ")
                    read_until(b"LOCAL TOOLS", start=len(data))
                    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 400, width, 0, 0))
                    child.send_signal(signal.SIGWINCH)
                elif cancel_context:
                    os.write(control[1], b"x")
                elif action == "signal":
                    child.send_signal(signal.SIGINT)
                elif action == "terminate":
                    child.send_signal(signal.SIGTERM)
                else:
                    os.write(master, action)
            read_until()
            assert child.wait(timeout=2) == code, (name, child.returncode, bytes(data))
            if mouse_handoff:
                assert answered, (name, "missing handoff request")
                if mouse_handoff in ("split", "large"):
                    assert select.select([slave], [], [], .25)[0], "following shell input was consumed"
                    assert os.read(slave, 4096) == pending_command, "reports leaked or shell input was damaged"
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
                assert b"\x1b[?1006l" in data and b"\x1b[?25h" in data, (name, "terminal cleanup missing")
                if compare_scrollback:
                    # Remove only viewer-owned footer and mode changes. All document bytes
                    # must have appeared once, in order, with no banner replay at EOF.
                    footer = b"Scroll or use arrows   Space: page   q: end"
                    normal = bytes(data)
                    for controls in (b"\x1b[?25l", b"\x1b[?1000h", b"\x1b[?1006h", b"\x1b[?1006l", b"\x1b[?1000l", b"\x1b[?25h", b"\x1b[5n", b"\r\x1b[2K", footer):
                        normal = normal.replace(controls, b"")
                    assert normal.replace(b"\r\n", b"\n") == ordinary, (name,"changed normal scrollback")
                    assert b"\x1b[?1049h" not in data
                elif mouse_handoff:
                    assert b"Help needs no API key" in data, (name, "missing end of help")
                elif name == "navigation":
                    assert b"\x1b[?1049h" in data and b"\x1b[?1049l" in data, "previous-page inspection failed"
                    assert data.count(b"Help needs no API key") == 1, "quit missed or duplicated the end"
                else:
                    assert b"Help needs no API key" not in data, (name, "early exit replayed unseen content")
            else:
                assert b"\x1b[?1049h" not in data, (name, "unexpected pager")
                if startup_failure:
                    assert b"Could not display help" in data, (name, "missing safe startup failure", bytes(data))
                    assert b"openai --help" in data, (name, "missing plain-help recovery")
                elif redirected != "stdout":
                    expected = b"Global flags:" if name == "nested-help" else b"GLOBAL FLAGS"
                    assert expected in data, (name, "missing help")
            if extra and "NO_COLOR" in extra:
                assert b"38;" not in data and b"38:" not in data, (name, "color opt-out ignored")
            assert not os.listdir(home), (name, "help changed state")
            print(f"PASS {name}")
            return bytes(data)
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            os.close(master)
            os.close(slave)
            for fd in control:
                os.close(fd)


ordinary = check("fits", height=600, extra={"NO_COLOR": "1"}, action=None, compare_scrollback=True)
check("end to bottom", action=b"G", extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("lowercase quit from top", action=b"q", extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("uppercase quit after scrolling", action=b" Q", extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("resize to bottom", extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("page to bottom", action=b" " * 12, extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("wheel to bottom", action=b"\x1b[<65;5;5M" * 100, extra={"NO_COLOR": "1"}, compare_scrollback=True)
# A direct launcher can cross a terminal margin inside shell quotes. Compare
# the whole native document with the fitting control to catch inserted newlines.
launcher = "/long path/" + "a" * 74 + "/openai"
ordinary = check("long launcher fits", launcher=launcher, width=80, height=600,
                 extra={"NO_COLOR": "1"}, action=None, compare_scrollback=True)
assert ("'" + launcher + "' help setup").encode() in ordinary
check("long launcher scrolls", launcher=launcher, width=80, action=b"q",
      extra={"NO_COLOR": "1"}, compare_scrollback=True)
check("delayed wheel reports and command typeahead", action=b"\x1b[<65;75;28M" * 200, mouse_handoff="split")
check("large wheel backlog and command typeahead", action=b"G", mouse_handoff="large")
check("interrupt during wheel handoff", action=b"G", mouse_handoff="interrupt", code=130)
check("terminate while the terminal withholds its reply", action=b"G", mouse_handoff="signal without reply", code=143)
check("terminal replies after SIGTERM delivery", action=b"G", mouse_handoff="reply after signal", code=143)
check("terminal omits status response", action=b"G", mouse_handoff="no response")
check("navigation")
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
