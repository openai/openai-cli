"""Run a Go test binary in a sized PTY, with an optional output observer."""
import errno
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import termios
import time


def run_terminal_test(binary, test, env, observe=None, timeout=60, columns=120, rows=45):
    master, slave = pty.openpty()
    child = None
    finished = False
    captured = bytearray()
    try:
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', rows, columns, 0, 0))
        child = subprocess.Popen(
            [binary, '-test.v', '-test.run=^' + test + '$'],
            stdin=subprocess.DEVNULL, stdout=slave, stderr=slave, env=env,
            start_new_session=True,
        )
        os.close(slave)
        slave = None
        deadline = time.monotonic() + timeout
        while True:
            if time.monotonic() >= deadline:
                raise TimeoutError('terminal tests timed out: ' + test)
            if select.select([master], [], [], 0.1)[0]:
                try:
                    chunk = os.read(master, 65536)
                except OSError as error:
                    if error.errno == errno.EIO:
                        break
                    raise
                if not chunk:
                    break
                captured.extend(chunk)
                if observe is not None:
                    observe(bytes(captured))
            elif child.poll() is not None:
                break
            elif observe is not None:
                # Observers can confirm a quiet interval before releasing a
                # fixture, without forcing the child to print another byte.
                observe(bytes(captured))
        code = child.wait(timeout=max(0.1, deadline - time.monotonic()))
        finished = True
        return code, bytes(captured)
    finally:
        os.close(master)
        if slave is not None:
            os.close(slave)
        if child is not None and not finished:
            # Own session only: do not leave test subprocesses after a timeout
            # or observer failure, and never target unrelated terminal sessions.
            try:
                os.killpg(child.pid, signal.SIGKILL)
            except ProcessLookupError:
                # The process group can exit between the check and killpg.
                pass
            child.wait()
