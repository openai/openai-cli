"""Run a Go test binary in a sized PTY, with an optional output observer."""
import base64
import errno
import fcntl
import os
import pty
import re
import select
import signal
import struct
import subprocess
import termios
import time


def kitty_images(captured):
    """Validate complete CLI Kitty transmissions and return their PNG bytes.

    One image can span several frames, including small Darwin tty frames.
    Interrupted transmissions belong in cancellation checks, not this parser.
    """
    frames = re.findall(rb'\x1b_G([^;\x1b]*);([^\x1b]*)\x1b\\', captured)
    assert len(frames) == captured.count(b'\x1b_G'), 'incomplete Kitty frame'
    images, payload = [], None
    for header, chunk in frames:
        pairs = [field.split(b'=', 1) for field in header.split(b',')]
        assert all(len(pair) == 2 for pair in pairs), 'invalid Kitty options'
        fields = dict(pairs)
        assert len(fields) == len(pairs), 'duplicate Kitty option'
        assert fields.get(b'q') == b'2', 'Kitty replies must stay disabled'
        assert fields.get(b'm') in (b'0', b'1'), 'invalid Kitty continuation'
        if payload is None:
            assert fields.get(b'a') == b'T' and fields.get(b'f') == b'100', 'expected a PNG transmission'
            payload = bytearray()
        else:
            assert set(fields) == {b'q', b'm'}, 'unexpected Kitty continuation options'
        assert chunk, 'empty Kitty image chunk'
        assert len(chunk) <= 4096 and len(chunk) % 4 == 0, 'invalid Kitty chunk size'
        payload.extend(chunk)
        if fields[b'm'] == b'0':
            images.append(base64.b64decode(payload, validate=True))
            payload = None
    assert payload is None, 'unfinished Kitty image'
    return images


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
