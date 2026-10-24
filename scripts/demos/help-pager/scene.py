"""Relay bare openai in a PTY; close its help viewer after the first page appears."""

import errno
import fcntl
import os
import pty
import select
import signal
import struct
import sys
import termios
import time

size = os.get_terminal_size(1)
original = termios.tcgetattr(1)
pid, master = pty.fork()
if pid == 0:
    fcntl.ioctl(1, termios.TIOCSWINSZ, struct.pack("HHHH", size.lines, size.columns, 0, 0))
    os.execvp("openai", ["openai"])
waited = False
try:
    relay = original[:]
    relay[1] &= ~termios.OPOST
    termios.tcsetattr(1, termios.TCSADRAIN, relay)
    deadline, quit_at = time.monotonic() + 15, None
    received = bytearray()
    while True:
        now = time.monotonic()
        if now > deadline:
            raise TimeoutError("help did not finish")
        if quit_at is not None and now >= quit_at:
            if os.write(master, b"q") != 1:
                raise OSError("short keyboard write")
            quit_at = float("inf")
        if not select.select([master], [], [], .05)[0]:
            continue
        try:
            chunk = os.read(master, 65536)
        except OSError as error:
            if error.errno != errno.EIO:
                raise
            break
        if not chunk:
            break
        received.extend(chunk)
        if sys.stdout.buffer.write(chunk) != len(chunk):
            raise OSError("short terminal relay write")
        sys.stdout.buffer.flush()
        if quit_at is None and b"q: quit" in received:
            if b"What are we making today?" not in received:
                raise AssertionError("banner is missing from the first page")
            quit_at = time.monotonic() + 3
    _, status = os.waitpid(pid, 0)
    waited = True
    if os.waitstatus_to_exitcode(status) != 0:
        raise RuntimeError("help failed")
finally:
    os.close(master)
    if not waited:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            # The child may have exited already; waitpid below still reaps it.
            pass
        os.waitpid(pid, 0)
    termios.tcsetattr(1, termios.TCSADRAIN, original)
