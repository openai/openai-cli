#!/usr/bin/env python3
"""Drive the real editor inside the shared recorder's terminal session."""

import errno
import fcntl
import json
import os
import pty
import re
import select
import signal
import struct
import sys
import termios
import time


FIXTURE = "Hello, tokens! 👋\nCafé."
CSI = re.compile(rb"\x1b\[[0-?]*[ -/]*[@-~]")
OSC = re.compile(rb"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
QUERY = re.compile(rb"\x1b\[\?7\$p|\x1b\]11;\?(?:\x07|\x1b\\)")


def stop_editor(pid, terminal):
    # Failed recordings need no remaining frames. Release terminal backpressure
    # before waiting for the owned session leader to finish on Darwin.
    os.close(terminal)
    for number in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(pid, number)
        except ProcessLookupError:
            # The owned process group can exit before this cleanup signal.
            pass
        deadline = time.monotonic() + 1
        while time.monotonic() < deadline:
            waited, value = os.waitpid(pid, os.WNOHANG)
            if waited:
                return os.waitstatus_to_exitcode(value)
            time.sleep(0.01)
    raise RuntimeError("editor did not exit within the recording cleanup deadline")


def drive():
    width = int(os.environ["DEMO_COLUMNS"])
    height = int(os.environ["DEMO_ROWS"])
    theme = os.environ["DEMO_THEME"]
    report_path = os.environ["DEMO_EDITOR_REPORT"]
    layout = os.environ.get("DEMO_EDITOR_LAYOUT", "options")
    if layout not in ("legacy", "options"):
        raise ValueError("DEMO_EDITOR_LAYOUT must be legacy or options")
    gate_read, gate_write = os.pipe()
    pid, terminal = pty.fork()
    if pid == 0:
        os.close(gate_write)
        if os.read(gate_read, 1) != b"1":
            os._exit(96)
        os.close(gate_read)
        os.execvp("openai", ["openai", "tokenizer"])
    os.close(gate_read)
    try:
        fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
        os.write(gate_write, b"1")
    except BaseException:
        os.close(gate_write)
        stop_editor(pid, terminal)
        raise
    else:
        os.close(gate_write)
    buffer = bytearray()
    queries = bytearray()
    events = []
    status = None
    eof = False

    def interrupt(number, _frame):
        raise InterruptedError(number)

    for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(number, interrupt)

    def write(data):
        view = memoryview(data)
        while view:
            count = os.write(terminal, view)
            if count <= 0:
                raise RuntimeError("terminal input stopped")
            view = view[count:]

    def pump(timeout):
        nonlocal status, eof, queries
        if not eof and select.select([terminal], [], [], timeout)[0]:
            try:
                data = os.read(terminal, 32768)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                # Forward actual CLI bytes. No fabricated frames enter the cast.
                sys.stdout.buffer.write(data)
                sys.stdout.buffer.flush()
                buffer.extend(data)
                del buffer[:-65536]
                queries.extend(data)
                while match := QUERY.search(queries):
                    if match.group().startswith(b"\x1b["):
                        write(b"\x1b[?7;1$y")
                    else:
                        color = b"ffff/ffff/ffff" if theme == "light" else b"1212/1414/1616"
                        write(b"\x1b]11;rgb:" + color + b"\x1b\\")
                    del queries[:match.end()]
                del queries[:-128]
            else:
                eof = True
        if status is None:
            waited, value = os.waitpid(pid, os.WNOHANG)
            if waited:
                status = os.waitstatus_to_exitcode(value)

    def wait_for(needle):
        deadline = time.monotonic() + 12
        while True:
            text = CSI.sub(b"", OSC.sub(b"", bytes(buffer))).decode("utf-8", "replace")
            if needle in text:
                return
            if status is not None or time.monotonic() >= deadline:
                raise RuntimeError("editor did not reach the requested state")
            pump(0.05)

    def pause(seconds=1.4):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            pump(max(0, min(0.05, deadline - time.monotonic())))

    def send(data):
        buffer.clear()
        write(data)

    def mark(name):
        events.append({"state": name, "observed_at": time.time()})
        pause()

    try:
        wait_for("Ctrl+C exit")
        pause(0.4)
        for character in "Hello, ":
            send(character.encode())
            pause(0.055)
        send(b"\x1b[200~" + "tokens! 👋".encode() + b"\x1b[201~\r\x1b[200~" +
             "Café.".encode() + b"\x1b[201~")
        wait_for("10 tokens" if layout == "options" else "10 tokens · 26 bytes")
        mark("text")
        send(b"\t\x1b[C")
        wait_for("[Token IDs]")
        mark("ids")
        if layout == "options":
            send(b"\r")
            wait_for("Choose view")
            mark("view-choice")
            send(b"\x1b[B\r")
        else:
            send(b"\x1b[C")
        wait_for("[Bytes]")
        wait_for("10 tokens · 26 bytes")
        mark("bytes")
        # The default encoding splits this emoji across token byte boundaries.
        if layout == "options":
            send(b"\t")
            wait_for("Enter details")
        send(b"\x1b[B" * 4 + b"\r")
        wait_for("Token details · exact bytes")
        wait_for("partial UTF-8")
        wait_for("ID 61138")
        wait_for("20 f0 9f 91")
        mark("details")
        send(b"\x1b")
        wait_for("Enter details")
        if layout == "options":
            send(b"\x1b")
            wait_for("Tab options")
            send(b"\t\x1b[B\r")
            wait_for("Choose tokenizer")
            mark("tokenizer-choice")
            send(b"\x1b[B")
        else:
            send(b"\t\x1b[B")
        wait_for("cl100k_base")
        pause(0.2)
        send(b"\r")
        wait_for("11 tokens · 26 bytes")
        send(b"\x1b[Z")
        tokenizer_label = "Tokenizer" if layout == "options" else "Encoding"
        wait_for(tokenizer_label + "  cl100k_base")
        mark("encoding")
        send(b"\x1bOP")
        wait_for("Tokenizer · controls")
        mark("controls")
        send(b"\x1b")
        wait_for(tokenizer_label + "  cl100k_base")
        pause(0.3)
        send(b"\x03")
        deadline = time.monotonic() + 5
        while status is None or not eof:
            if time.monotonic() >= deadline:
                raise RuntimeError("editor did not exit after Ctrl+C")
            pump(0.05)
        if status != 130:
            raise RuntimeError(f"editor returned {status}, expected 130")
        with open(report_path, "w", encoding="utf-8") as report:
            json.dump({"input": FIXTURE, "input_bytes": len(FIXTURE.encode()), "theme": theme,
                       "columns": width, "rows": height, "layout": layout,
                       "input_actions": {"typed": "Hello, ", "pasted": ["tokens! 👋", "Café."], "newline": "Enter"},
                       "states": events, "exit_status": status}, report, indent=2)
            report.write("\n")
        return status
    finally:
        if status is None:
            status = stop_editor(pid, terminal)
        else:
            os.close(terminal)


if __name__ == "__main__":
    try:
        sys.exit(drive())
    except InterruptedError as error:
        sys.exit(128 + int(error.args[0]))
    except Exception as error:
        print(f"Editor recording failed: {error}", file=sys.stderr)
        sys.exit(97)
