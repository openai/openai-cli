#!/usr/bin/env python3
"""Relay exact CLI bytes and advance only after loaded results and controls appear."""

import errno
import fcntl
import hashlib
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


def main():
    if len(sys.argv) < 2:
        raise SystemExit("usage: scene.py COMMAND [ARG ...]")
    if not all(os.isatty(fd) for fd in (0, 1, 2)):
        raise SystemExit("scene driver requires terminal stdin, stdout, and stderr")
    ready_marker = os.environ.get("DEMO_READY_MARKER", "").encode()
    size = os.get_terminal_size(1)
    outer_initial = termios.tcgetattr(1)
    pid, master = pty.fork()
    if pid == 0:
        try:
            fcntl.ioctl(1, termios.TIOCSWINSZ, struct.pack("HHHH", size.lines, size.columns, 0, 0))
            os.execvp(sys.argv[1], sys.argv[1:])
        except Exception:
            os._exit(99)
    waited = False
    try:
        relay_mode = outer_initial[:]
        # Preserve the child's LF bytes during incremental terminal redraws.
        relay_mode[1] &= ~termios.OPOST
        termios.tcsetattr(1, termios.TCSADRAIN, relay_mode)
        relay_digest = hashlib.sha256()
        pending_key = None
        inputs = []
        started = time.monotonic()
        deadline = started + 15
        recent = b""
        while True:
            now = time.monotonic()
            if now > deadline:
                raise TimeoutError("CLI scene did not exit within 15 seconds")
            if pending_key is not None and now >= pending_key[0]:
                key = pending_key[1]
                if os.write(master, key.encode()) != 1:
                    raise OSError("short keyboard write")
                inputs.append({"time": now - started, "key": key})
                pending_key = None
                recent = b""
            if not select.select([master], [], [], 0.05)[0]:
                continue
            try:
                chunk = os.read(master, 65536)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                break
            if not chunk:
                break
            relay_digest.update(chunk)
            if sys.stdout.buffer.write(chunk) != len(chunk):
                raise OSError("short terminal relay write")
            sys.stdout.buffer.flush()
            recent = (recent + chunk)[-65536:]
            plain = re.sub(rb"\x1b\[[0-?]*[ -/]*[@-~]", b"", recent)
            ready = ready_marker in plain if ready_marker else any(
                marker in plain for marker in (b"20261007", b"No results."))
            if pending_key is None and ready and b"q: quit" in plain:
                pending_key = (time.monotonic() + 1.5, "q")
        _, status = os.waitpid(pid, 0)
        waited = True
        exit_status = os.waitstatus_to_exitcode(status)
        evidence_path = os.environ.get("DEMO_RELAY_EVIDENCE")
        if evidence_path:
            with open(evidence_path, "x", encoding="utf-8") as evidence:
                json.dump({"sha256": relay_digest.hexdigest(), "exit_status": exit_status,
                           "navigation": "pager", "inputs": inputs}, evidence)
                evidence.write("\n")
        return exit_status
    finally:
        try:
            os.close(master)
            if not waited:
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os.waitpid(pid, 0)
        finally:
            termios.tcsetattr(1, termios.TCSADRAIN, outer_initial)


if __name__ == "__main__":
    status = main()
    raise SystemExit(status if status >= 0 else 128 - status)
