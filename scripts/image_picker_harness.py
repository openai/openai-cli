#!/usr/bin/env python3
"""Shared PTY and synthetic loopback fixtures for picker process checks."""
from contextlib import ExitStack
from functools import partial
import ast
import base64
import errno
import fcntl
import http.server
import json
import locale
import os
import pty
import re
import select
import selectors
import shlex
import signal
import struct
import stat
import subprocess
import sys
import termios
import time
import unicodedata

# Public fixtures used by the dynamically loaded picker process checks.
__all__ = [
    'ANSI', 'DOWN', 'END', 'Fixture', 'HOME', 'PNG', 'PRINT', 'Terminal', 'UP',
    'check_receipts', 'generate', 'http', 'printed_flags', 'ready', 'screen',
    'wait_for_request', 'wait_resumed', 'wait_submit_guard',
]

PNG = base64.b64decode('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP438AAAAQBAYDFKhhdAAAAAElFTkSuQmCC')
ANSI = re.compile(r'\x1b\][^\x07]*?(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]')
DOWN, UP, HOME, END = b'\x1b[B', b'\x1b[A', b'\x1b[H', b'\x1b[F'
PRINT = b'\x10'  # Ctrl+P prints the complete command without making a request.

SCREEN_ANSI = re.compile(rb'\x1b\][^\x07]*?(?:\x07|\x1b\\)|\x1b_[\s\S]*?\x1b\\|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[78]')


def screen(raw, width=96, height=28, history=None):
    """Receive inline-panel wrapping, saved cursors, relative moves and erases."""
    raw += b'\x1b[0m'
    rows = [[' '] * width for _ in range(height)]
    y = x = 0
    saved_position = (0, 0)
    pending = False
    position = 0

    def newline():
        nonlocal y
        y += 1
        if y >= height:
            previous=rows.pop(0)
            if history is not None:
                history.append(''.join(previous).rstrip())
            rows.append([' '] * width)
            y = height - 1

    for match in SCREEN_ANSI.finditer(raw):
        text = raw[position:match.start()].decode('utf-8', errors='replace')
        for char in text:
            if char == '\r':
                x, pending = 0, False
            elif char == '\n':
                newline()
                pending = False
            elif char >= ' ' and not unicodedata.combining(char):
                cells = 2 if unicodedata.east_asian_width(char) in ('W', 'F') else 1
                if pending or x + cells > width:
                    newline()
                    x = 0
                if 0 <= y < height and 0 <= x < width:
                    rows[y][x] = char
                pending = x + cells >= width
                x = min(width - 1, x + cells)
        control = match.group()
        if control == b'\x1b7':
            saved_position = (y, x)
        elif control == b'\x1b8':
            y, x = saved_position
            pending = False
        elif control == b'\x1b[2J':
            rows = [[' '] * width for _ in range(height)]
        elif control.startswith(b'\x1b['):
            final = control[-1:]
            values = control[2:-1].decode()
            if not values or all(c in '0123456789;' for c in values):
                numbers = [int(v or '1') for v in values.split(';')]
                if final in (b'H', b'f'):
                    y = min(height - 1, max(0, numbers[0] - 1))
                    x = min(width - 1, max(0, (numbers[1] if len(numbers) > 1 else 1) - 1))
                    pending = False
                elif final == b'G':
                    x, pending = min(width - 1, max(0, numbers[0] - 1)), False
                elif final in (b'A', b'B'):
                    y = max(0, min(height - 1, y + numbers[0] * (-1 if final == b'A' else 1)))
                    pending = False
                elif final == b'K':
                    rows[y][x:] = [' '] * (width - x)
                elif final == b'J':
                    rows[y][x:] = [' '] * (width - x)
                    for row in range(y + 1, height):
                        rows[row] = [' '] * width
                elif final == b'X':
                    for col in range(x, min(width, x + numbers[0])):
                        rows[y][col] = ' '
        position = match.end()
    return '\n'.join(''.join(row).rstrip() for row in rows).encode()



def cleanup_fixture_resources(*actions):
    """Attempt every owned cleanup action; ordinary failures must not replace control exceptions."""
    def close(action, _kind, pending, traceback):
        try:
            action()
        except Exception as error:
            if isinstance(pending, BaseException) and not isinstance(pending, Exception):
                # Keep every cleanup diagnostic, the original status, and the final error chain.
                pending.fixture_cleanup_errors = (*getattr(pending, "fixture_cleanup_errors", ()), error)
                raise pending.with_traceback(traceback) from error
            raise
        return False

    pending = sys.exc_info()
    stack = ExitStack()
    for action in reversed(actions):
        stack.push(partial(close, action))
    stack.__exit__(*pending)


class Terminal:
    def __init__(self, binary, args, env, width=80, height=24,
                 input_file=None, output_file=None, controlling_terminal=False, stderr_file=None,
                 max_output_bytes=None, max_events=None, max_event_bytes=None, deadline=None,
                 retain_child_identity=False, cwd=None):
        self.max_output_bytes = max_output_bytes
        self.max_events = max_events
        self.max_event_bytes = max_event_bytes
        self.deadline = deadline
        self.event_bytes = 0
        self.eof = False
        self._slave_closed = False
        self._master_closed = False
        self.retain_child_identity = retain_child_identity
        self._identity_lost = False
        if retain_child_identity and not all(hasattr(os, name) for name in ('waitid', 'WNOWAIT', 'P_PID')):
            raise RuntimeError('bounded PTY cleanup requires waitid/WNOWAIT support')
        for bound in (max_output_bytes, max_events, max_event_bytes):
            if bound is not None and bound <= 0:
                raise ValueError('fixture bounds must be positive')
        if deadline is not None and time.monotonic() >= deadline:
            raise TimeoutError('PTY fixture deadline expired before launch')
        self.master, self.slave = pty.openpty()
        self.initial = termios.tcgetattr(self.slave)
        self.initial_size = (width, height)
        self.resize(width, height, notify=False)
        self.started = time.monotonic()
        self.events, self.raw = [], bytearray()
        try:
            self.child = subprocess.Popen([binary, *args], stdin=self.slave if input_file is None else input_file,
                                          stdout=self.slave if output_file is None else output_file,
                                          stderr=self.slave if stderr_file is None else stderr_file,
                                          env=env, cwd=cwd, start_new_session=True,
                                          preexec_fn=(lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
                                          if controlling_terminal else None)
        except Exception:
            os.close(self.master)
            os.close(self.slave)
            raise

    def resize(self, width, height, notify=True):
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
        if notify:
            self._event('r', f'{width}x{height}')
            os.kill(self.child.pid, signal.SIGWINCH)

    def size(self):
        height, width, _, _ = struct.unpack(
            "HHHH", fcntl.ioctl(self.master, termios.TIOCGWINSZ, bytes(8)))
        return width, height

    def _event(self, kind, text):
        size = len(text.encode('utf-8'))
        if self.max_events is not None and len(self.events) >= self.max_events:
            raise RuntimeError('PTY fixture event count exceeded its bound')
        if self.max_event_bytes is not None and self.event_bytes + size > self.max_event_bytes:
            raise RuntimeError('PTY fixture event bytes exceeded their bound')
        self.events.append([time.monotonic()-self.started, kind, text])
        self.event_bytes += size

    def read(self, duration=0.05):
        if self.deadline is not None:
            remaining = self.deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('PTY fixture deadline expired')
            duration = min(duration, remaining)
        if self.eof:
            return
        if select.select([self.master], [], [], duration)[0]:
            count = 65536
            if self.max_output_bytes is not None:
                count = min(count, self.max_output_bytes - len(self.raw) + 1)
            try:
                chunk = os.read(self.master, count)
            except OSError as error:
                if error.errno == errno.EIO:
                    self.eof = True
                    return
                raise
            if not chunk:
                self.eof = True
                return
            if self.max_output_bytes is not None and len(self.raw) + len(chunk) > self.max_output_bytes:
                raise RuntimeError('PTY fixture output exceeded its bound')
            self._event('o', chunk.decode('utf-8', 'replace'))
            self.raw.extend(chunk)
            # A PTY has no emulator to answer terminal capability queries.
            for query, reply in [(b'\x1b[6n', b'\x1b[1;1R'),
                                 (b'\x1b]11;?\x07', b'\x1b]11;rgb:1818/1818/1818\x07'),
                                 (b'\x1b[c', b'\x1b[?1;2c'),
                                 (b'\x1b[0c', b'\x1b[?1;2c')]:
                if query in chunk:
                    os.write(self.master, reply)

    def text(self):
        return ANSI.sub('', self.raw.decode('utf-8', 'replace'))

    def running(self):
        if not self.retain_child_identity:
            return self.child.poll() is None
        if self.child.returncode is not None:
            return False
        try:
            result = os.waitid(os.P_PID, self.child.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
        except ChildProcessError:
            self._identity_lost = True
            raise RuntimeError('PTY child identity was reaped outside its owner')
        return result is None or result.si_pid == 0

    def wait(self, marker, timeout=12, after=0):
        deadline = time.monotonic()+timeout
        while marker not in ANSI.sub('', self.raw[after:].decode('utf-8', 'replace')):
            self.read()
            if not self.running() or time.monotonic() > deadline:
                raise AssertionError(f'missing {marker!r}; exit={self.child.returncode}; tail={self.text()[-1800:]}')

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
        def stop_child():
            # A retained, unreaped leader keeps its PID reserved, even after exit.
            signal_owned = (self.child.returncode is None and not self._identity_lost
                            if self.retain_child_identity else self.child.poll() is None)
            if signal_owned:
                try:
                    os.killpg(self.child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    # The process can exit between poll() and killpg().
                    pass

        def close_master():
            if not self._master_closed:
                os.close(self.master)
                self._master_closed = True

        def close_slave():
            if not self._slave_closed:
                os.close(self.slave)
                self._slave_closed = True

        # Darwin needs the controlling PTY handles closed before the final child wait.
        cleanup_fixture_resources(stop_child, close_master, close_slave, partial(self.child.wait, timeout=5))

    def drain_eof(self, timeout=2):
        """Observe PTY EOF after the child exits, without the parent's slave holding it open."""
        if self.running():
            raise RuntimeError('cannot drain PTY EOF before child exit')
        if not self._slave_closed:
            os.close(self.slave)
            self._slave_closed = True
        until = time.monotonic() + timeout
        while not self.eof:
            if time.monotonic() >= until:
                raise TimeoutError('PTY fixture did not reach EOF')
            self.read(min(0.05, max(0, until - time.monotonic())))
        self.child.wait(timeout=max(0.001, until - time.monotonic()))

    def save(self, destination):
        destination.with_name(destination.name + '.raw').write_bytes(self.raw)
        header = dict(version=2, width=self.initial_size[0], height=self.initial_size[1],
                      title='Local image picker; synthetic API', env={'TERM':'xterm-256color'})
        destination.with_name(destination.name + '.cast').write_text('\n'.join(json.dumps(v) for v in [header, *self.events])+'\n')


def capture_fixture(argv, *, env, cwd=None, timeout=10, limit=1048576, owned_processes=None):
    """Capture a finite synthetic fixture command with one shared stdout/stderr allowance."""
    if timeout <= 0 or limit <= 0:
        raise ValueError('fixture command bounds must be positive')
    until = time.monotonic() + timeout
    streams = {'stdout': bytearray(), 'stderr': bytearray()}
    eof = set()
    selector = selectors.DefaultSelector()
    process = None
    try:
        process = subprocess.Popen(argv, cwd=cwd, env=env, stdin=subprocess.DEVNULL,
                                   stdout=subprocess.PIPE, stderr=subprocess.PIPE, start_new_session=True)
        if owned_processes is not None:
            owned_processes.append(process)
        process.fixture_stdout_eof = False
        process.fixture_stderr_eof = False
        for name, pipe in (('stdout', process.stdout), ('stderr', process.stderr)):
            os.set_blocking(pipe.fileno(), False)
            selector.register(pipe, selectors.EVENT_READ, name)
        while len(eof) != 2 or process.poll() is None:
            remaining = until - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('fixture command or pipe drain exceeded its deadline')
            for key, _ in selector.select(min(0.05, remaining)):
                size = sum(map(len, streams.values()))
                data = os.read(key.fd, min(65536, limit - size + 1))
                if not data:
                    eof.add(key.data)
                    setattr(process, 'fixture_' + key.data + '_eof', True)
                    selector.unregister(key.fileobj)
                elif size + len(data) > limit:
                    raise RuntimeError('fixture command output exceeded its bound')
                else:
                    streams[key.data].extend(data)
        result = subprocess.CompletedProcess(argv, process.wait(timeout=0),
                                             bytes(streams['stdout']), bytes(streams['stderr']))
        result.stdout_eof = 'stdout' in eof
        result.stderr_eof = 'stderr' in eof
        return result
    finally:
        actions = [selector.close]
        if process is not None:
            process.fixture_cleanup_complete = False

            def stop_child():
                # Do not poll: an exited, unreaped leader still reserves the owned group identity.
                if process.returncode is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        # The owned process group can exit before the cleanup signal arrives.
                        pass

            actions.extend((process.stdout.close, process.stderr.close, stop_child, partial(process.wait, timeout=2)))
        cleanup_fixture_resources(*actions)
        if process is not None:
            process.fixture_cleanup_complete = True


def read_fixture_file(path, limit=65536):
    """Read a small owned fixture file without following a replaced symlink."""
    descriptor = os.open(path, os.O_RDONLY | getattr(os, 'O_NOFOLLOW', 0) | getattr(os, 'O_NONBLOCK', 0))
    with os.fdopen(descriptor, 'rb') as handle:
        info = os.fstat(handle.fileno())
        if not stat.S_ISREG(info.st_mode) or info.st_size > limit:
            raise RuntimeError('fixture file is not regular or exceeds its bound')
        data = handle.read(limit + 1)
        if len(data) > limit:
            raise RuntimeError('fixture file grew beyond its bound')
        return data


def fixture_utf8_locale(preferred, env, owned_processes=None):
    """Use a working inherited UTF-8 locale, or select one installed on this fixture host."""
    original = locale.setlocale(locale.LC_CTYPE)

    def valid(value):
        if not value:
            return False
        try:
            locale.setlocale(locale.LC_CTYPE, value)
            return locale.nl_langinfo(locale.CODESET).upper().replace('-', '') == 'UTF8'
        except locale.Error:
            return False
        finally:
            locale.setlocale(locale.LC_CTYPE, original)

    if valid(preferred):
        return preferred, 'inherited'
    available = capture_fixture(['/usr/bin/locale', '-a'], env=dict(env, LC_ALL='C'),
                                timeout=5, owned_processes=owned_processes)
    if available.returncode != 0 or available.stderr:
        raise RuntimeError('cannot enumerate installed fixture locales')
    choices = available.stdout.decode('utf-8').splitlines()
    choices.sort(key=lambda value: value.lower().replace('-', '') not in ('c.utf8', 'en_us.utf8'))
    for value in choices:
        if valid(value):
            return value, 'installed'
    raise RuntimeError('an installed UTF-8 locale is required for this fixture')


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


def wait_resumed(terminal, mark, prompt):
    """Require a fresh completed request and the retained workspace form."""
    terminal.wait('Generating image', after=mark)
    busy = terminal.raw.index(b'Generating image', mark)
    terminal.wait('Saved in ', after=busy)
    saved = terminal.raw.index(b'Saved in ', busy)
    terminal.wait('Ctrl+C exit', after=saved)
    width, height = terminal.size()
    frame = screen(bytes(terminal.raw), width, height)
    for marker in ('Create image', 'Settings', 'Command', 'Ctrl+C exit'):
        assert marker.encode() in frame, (marker, frame)
    assert b'Generating image' not in frame, 'workspace remains busy'
    for line in prompt.splitlines():
        if line:
            terminal.wait(line, after=mark)
            # Use the same cell receiver for wide and joined Unicode characters.
            visible = screen(line.encode(), width, height).rstrip()
            assert visible in frame, (line, frame)
    assert b'Saved image:' not in terminal.raw[mark:], 'receipt escaped the active workspace'


def check_receipts(terminal, files):
    """Require each saved path exactly once after workspace closure."""
    files = tuple(files)
    # Workspace teardown can return the cursor without emitting a newline.
    text = terminal.text().replace('\r\n', '\n').replace('\r', '\n')
    assert text.count('Saved image: ') == len(files), text
    quoted = re.findall(r'^Saved image: ("(?:[^"\\\r\n]|\\.)*")\r?$', text, re.MULTILINE)
    assert len(quoted) == len(files), text
    # Go %+q uses ASCII escapes, including \x and \U forms absent from JSON.
    paths = [ast.literal_eval(value) for value in quoted]
    for path in files:
        assert paths.count(str(path)) == 1, (str(path), paths)


def wait_submit_guard(terminal):
    """Drain output while the restored draft's 750 ms submit guard expires."""
    until = time.monotonic() + 1
    while time.monotonic() < until:
        terminal.read(0.02)


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
