#!/usr/bin/env python3
"""Shared PTY and synthetic loopback fixtures for picker process checks."""
import base64
import errno
import fcntl
import http.server
import json
import os
import pty
import re
import select
import shlex
import signal
import struct
import subprocess
import sys
import termios
import time

# Public fixtures used by the dynamically loaded picker process checks.
__all__ = [
    'ANSI', 'DOWN', 'END', 'Fixture', 'HOME', 'PNG', 'PRINT', 'Terminal', 'UP',
    'generate', 'http', 'printed_flags', 'ready', 'wait_for_request',
]

PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC')
ANSI = re.compile(r'\x1b\][^\x07]*?(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]')
DOWN, UP, HOME, END = b'\x1b[B', b'\x1b[A', b'\x1b[H', b'\x1b[F'
PRINT = b'\x10'  # Ctrl+P prints the complete command without making a request.


class Terminal:
    def __init__(self, binary, args, env, width=80, height=24,
                 input_file=None, output_file=None, controlling_terminal=False):
        self.master, self.slave = pty.openpty()
        self.initial = termios.tcgetattr(self.slave)
        self.initial_size = (width, height)
        self.resize(width, height, notify=False)
        self.started = time.monotonic()
        self.events, self.raw = [], bytearray()
        self.child = subprocess.Popen([binary, *args], stdin=self.slave if input_file is None else input_file,
                                      stdout=self.slave if output_file is None else output_file, stderr=self.slave,
                                      env=env, start_new_session=True,
                                      preexec_fn=(lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
                                      if controlling_terminal else None)

    def resize(self, width, height, notify=True):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
        if notify:
            self.events.append([time.monotonic()-self.started, 'r', f'{width}x{height}'])
            os.kill(self.child.pid, signal.SIGWINCH)

    def read(self, duration=0.05):
        if select.select([self.master], [], [], duration)[0]:
            try:
                chunk = os.read(self.master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    return
                raise
            self.raw.extend(chunk)
            self.events.append([time.monotonic()-self.started, 'o', chunk.decode('utf-8', 'replace')])
            # A PTY has no emulator to answer terminal capability queries.
            for query, reply in [(b'\x1b[6n', b'\x1b[1;1R'),
                                 (b'\x1b]11;?\x07', b'\x1b]11;rgb:1818/1818/1818\x07'),
                                 (b'\x1b[c', b'\x1b[?1;2c'),
                                 (b'\x1b[0c', b'\x1b[?1;2c')]:
                if query in chunk:
                    os.write(self.master, reply)

    def text(self):
        return ANSI.sub('', self.raw.decode('utf-8', 'replace'))

    def wait(self, marker, timeout=12, after=0):
        deadline = time.monotonic()+timeout
        while marker not in ANSI.sub('', self.raw[after:].decode('utf-8', 'replace')):
            self.read()
            if self.child.poll() is not None or time.monotonic() > deadline:
                raise AssertionError(f'missing {marker!r}; exit={self.child.poll()}; tail={self.text()[-1800:]}')

    def send(self, value):
        os.write(self.master, value)

    def finish(self, expected, timeout=12, expect_picker=True):
        deadline = time.monotonic()+timeout
        while self.child.poll() is None:
            self.read()
            if time.monotonic() > deadline:
                raise AssertionError('picker did not exit: '+self.text()[-1200:])
        for _ in range(3):
            self.read(0.03)
        assert self.child.returncode == expected, (self.child.returncode, expected, self.text()[-2000:])
        restored = termios.tcgetattr(self.slave)
        # Darwin sets transient PENDIN when restoring canonical input, even for
        # a plain tty.setraw/tcsetattr round trip. Ignore only that kernel bit.
        if sys.platform == 'darwin':
            restored[3] &= ~termios.PENDIN
            self.initial[3] &= ~termios.PENDIN
        assert restored == self.initial, ('terminal input mode was not restored', self.initial, restored)
        assert not re.search(rb'\x1b\[\?(?:47|1047|1049)[hl]', self.raw), 'command used an alternate screen'
        assert not re.search(rb'\x1b\[[23]J', self.raw), 'command cleared the screen or scrollback'
        if expect_picker:
            for mode in (b'2004', b'25'):
                enabled = self.raw.rfind(b'\x1b[?'+mode+b'h')
                disabled = self.raw.rfind(b'\x1b[?'+mode+b'l')
                if mode == b'2004':
                    assert enabled >= 0 and disabled > enabled, 'bracketed paste was not restored'
                else:
                    assert enabled > disabled >= 0, 'cursor visibility was not restored'
        else:
            assert b'\x1b[?2004h' not in self.raw, 'direct command entered the picker'

    def close(self):
        try:
            if self.child.poll() is None:
                try:
                    os.killpg(self.child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    # The process can exit between poll() and killpg().
                    pass
        finally:
            # Darwin can keep a session leader exiting until the controlling
            # PTY's parent handles close. Close them before waiting to reap it.
            os.close(self.master)
            os.close(self.slave)
        self.child.wait(timeout=5)

    def save(self, destination):
        destination.with_suffix('.raw').write_bytes(self.raw)
        header = dict(version=2, width=self.initial_size[0], height=self.initial_size[1],
                      title='Local image picker; synthetic API', env={'TERM':'xterm-256color'})
        destination.with_suffix('.cast').write_text('\n'.join(json.dumps(v) for v in [header, *self.events])+'\n')


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        self.server.requests.append(body)
        self.server.request_started.set()
        if self.server.mode == 'slow':
            self.server.release.wait(10)
        status = 401 if self.server.mode == 'error' else 200
        result = ({'error': {'message':'Synthetic authentication failure', 'type':'invalid_request_error'}}
                  if status == 401 else {'created':1700000000, 'data':[{'b64_json':base64.b64encode(PNG).decode()}]})
        data = json.dumps(result).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        try:
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            # Cancellation tests intentionally disconnect before the reply.
            pass


def ready(terminal):
    terminal.wait('Prompt')
    terminal.wait('openai images generate')


def generate(terminal, focus='prompt'):
    if focus == 'command':
        terminal.send(b'\t\t')
    if focus == 'shortcut':
        terminal.send(b'\t\x07')  # Ctrl+G from options.
    else:
        terminal.send(b'\r')


def wait_for_request(terminal, started, timeout=8):
    # Keep draining the PTY while Tea renders its final frame and restores the
    # terminal. Waiting only on the server event can fill the PTY output buffer
    # and prevent the child from reaching the API request at all.
    deadline = time.monotonic() + timeout
    while not started.is_set():
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise AssertionError('no request reached fixture; tail='+terminal.text()[-1800:])
        terminal.read(min(0.05, remaining))
        if terminal.child.poll() is not None and not started.is_set():
            raise AssertionError(f'child exited before request; exit={terminal.child.returncode}; tail={terminal.text()[-1800:]}')


def printed_flags(terminal):
    """Read the full command printed after terminal restoration, without running it."""
    # Inline rendering stays in the main buffer. Bracketed-paste teardown marks
    # terminal restoration; only subsequent output is the submitted command.
    assert b'\x1b[?2004l' in terminal.raw, 'picker did not restore terminal input'
    after_ui = terminal.raw.rsplit(b'\x1b[?2004l', 1)[-1].decode('utf-8', 'replace')
    lines = ANSI.sub('', after_ui).splitlines()
    commands = [line for line in lines if line.startswith('openai images generate ')]
    assert len(commands) == 1, ('expected one printed command', lines)
    arguments = shlex.split(commands[0])
    assert len(arguments[3:]) % 2 == 0, arguments
    return dict(zip(arguments[3::2], arguments[4::2]))
