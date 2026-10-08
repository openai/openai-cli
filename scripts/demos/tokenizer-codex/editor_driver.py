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


def drive():
    width = int(os.environ["DEMO_COLUMNS"])
    height = int(os.environ["DEMO_ROWS"])
    theme = os.environ["DEMO_THEME"]
    report_path = os.environ["DEMO_EDITOR_REPORT"]
    gate_read, gate_write = os.pipe()
    pid, terminal = pty.fork()
    if pid == 0:
        os.close(gate_write)
        if os.read(gate_read, 1) != b"1":
            os._exit(96)
        os.close(gate_read)
        os.execvp("openai", ["openai", "tokenizer"])
    os.close(gate_read)
    fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
    os.write(gate_write, b"1")
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
        send(b"\x1b[200~" + "tokens! 👋\nCafé.".encode() + b"\x1b[201~")
        wait_for(f"tokens · {len(FIXTURE.encode())} bytes")
        mark("text")
        send(b"\t\x1b[C")
        wait_for("[Token IDs]")
        mark("ids")
        send(b"\x1b[C")
        wait_for("[Bytes]")
        mark("bytes")
        # The default encoding splits this emoji across token byte boundaries.
        send(b"\x1b[B" * 4 + b"\r")
        wait_for("Token details · exact bytes")
        wait_for("partial UTF-8")
        mark("details")
        send(b"\x1b")
        wait_for("Enter details")
        send(b"\t\x1b[B")
        wait_for("cl100k_base")
        pause(0.2)
        send(b"\r")
        wait_for(f"tokens · {len(FIXTURE.encode())} bytes")
        send(b"\x1b[Z")
        wait_for("Encoding  cl100k_base")
        mark("encoding")
        send(b"\x1bOP")
        wait_for("Tokenizer · controls")
        mark("controls")
        send(b"\x1b")
        wait_for("Encoding  cl100k_base")
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
                       "columns": width, "rows": height, "states": events, "exit_status": status}, report, indent=2)
            report.write("\n")
        return status
    finally:
        if status is None:
            for number in (signal.SIGTERM, signal.SIGKILL):
                try:
                    os.killpg(pid, number)
                except ProcessLookupError:
                    break
                deadline = time.monotonic() + 1
                while status is None and time.monotonic() < deadline:
                    waited, value = os.waitpid(pid, os.WNOHANG)
                    if waited:
                        status = os.waitstatus_to_exitcode(value)
                    else:
                        time.sleep(0.01)
                if status is not None:
                    break
            if status is None:
                os.waitpid(pid, 0)
        os.close(terminal)


if __name__ == "__main__":
    try:
        sys.exit(drive())
    except InterruptedError as error:
        sys.exit(128 + int(error.args[0]))
    except Exception as error:
        print(f"Editor recording failed: {error}", file=sys.stderr)
        sys.exit(97)
