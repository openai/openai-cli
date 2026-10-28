#!/usr/bin/env python3
"""Exercise default list paging through public commands and private PTYs."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import termios
import time

sys.dont_write_bytecode = True
HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]


def module(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    loaded = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(loaded)
    return loaded


fixture = module('viewer_fixture', HERE / 'fixture.py')
harness = module('viewer_terminal', ROOT / 'scripts/image_picker_harness.py')
STATUS = re.compile(rb'__VIEWER_STATUS_([0-9]+)__')
ORIGINAL = '__ORIGINAL_HISTORY__'
OBSERVER = 'printf "__ORIGINAL_HISTORY__\\n"; "$@"; code=$?; printf "\\n__VIEWER_STATUS_%s__\\n" "$code"; IFS= read -r acknowledgement; exit "$code"'
PID_OBSERVER = OBSERVER.replace('"$@"; code=$?',
    '/bin/bash --noprofile --norc -c \'printf "__VIEWER_PID_%s__\\n" "$$"; exec "$@"\' viewer-cli "$@"; code=$?')


def requests(server):
    with server.lock:
        return list(server.requests)


def drain(terminal, duration=0.15):
    deadline = time.monotonic() + duration
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline - time.monotonic())))


class Session:
    def __init__(self, binary, output, server, args=(), *, width=98, height=14, extra_env=None, pid_handshake=False):
        self.output, self.server, self.keys = output, server, []
        self.binary = Path(binary).resolve()
        output.mkdir()
        self.home = tempfile.TemporaryDirectory(prefix='viewer-observer-')
        self.env = {'PATH': '/usr/bin:/bin', 'HOME': self.home.name, 'XDG_CONFIG_HOME': self.home.name,
                    'LANG': 'en_US.UTF-8', 'TERM': 'xterm-256color', 'TERM_PROGRAM': 'Apple_Terminal',
                    'NO_COLOR': '1', 'FORCE_COLOR': '0', 'GOMAXPROCS': '2',
                    'OPENAI_API_KEY': 'synthetic-pager-key', 'OPENAI_ADMIN_KEY': 'synthetic-pager-admin-key',
                    'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1',
                    'OPENAI_CLI_NO_UPDATE_CHECK': '1', 'OPENAI_CLI_NO_AUTO_UPDATE': '1'}
        self.env.update(extra_env or {})
        explicit_format = any(value == '--format' or value.startswith('--format=') for value in args)
        self.expect_viewer = not explicit_format and not self.env.get('CI') and self.env.get('TERM') != 'dumb'
        command = (['admin', 'organization', 'projects', 'list'] if server.resource == 'projects' else [server.resource, 'list']) + ['--max-items', '-1', *args]
        if server.resource != 'models':
            command.extend(['--limit', str(server.rows)])
        self.terminal = harness.Terminal('/bin/bash', ['--noprofile', '--norc', '-c', PID_OBSERVER if pid_handshake else OBSERVER,
                        'viewer-observer', str(binary), *command], self.env, width=width, height=height)
        self.stage = 0

    def wait(self, marker, timeout=8):
        self.terminal.wait(marker, timeout=timeout)

    def send(self, key):
        value = key.encode()
        written = os.write(self.terminal.master, value)
        if written != len(value):
            raise AssertionError('short input write')
        self.terminal._event('i', key)
        self.keys.append(key)

    def count(self, expected):
        drain(self.terminal)
        assert len(requests(self.server)) == expected, (expected, requests(self.server))
        assert self.server.maximum_inflight <= 1, 'requests overlapped'

    def visible(self):
        drain(self.terminal, 0.04)
        self.stage += 1
        base = self.output / f'stage-{self.stage:02d}'
        self.terminal.save(base)
        text = base.with_suffix('.txt')
        conversion = subprocess.run([ASCIINEMA, 'convert', '--overwrite', '-f', 'txt',
                                     str(base.with_suffix('.cast')), str(text)],
                                    capture_output=True, timeout=5,
                                    env={'PATH': '/usr/bin:/bin', 'HOME': self.home.name,
                                         'ASCIINEMA_CONFIG_HOME': self.home.name + '/config',
                                         'ASCIINEMA_STATE_HOME': self.home.name + '/state'})
        assert conversion.returncode == 0, conversion.stderr.decode('utf-8', 'replace')
        return text.read_text()

    def wait_visible(self, marker, *, absent=None):
        deadline = time.monotonic() + 8
        while True:
            self.terminal.read()
            text = self.visible()
            if marker in text and (absent is None or absent not in text):
                return text
            assert self.terminal.child.poll() is None and time.monotonic() < deadline, (marker, absent, text)

    def loaded(self, page):
        self.wait_visible(f'page-{page:03d}-item-001.jsonl')
        self.wait_visible('q: quit')

    def finish(self, expected, identifiers=()):
        deadline = time.monotonic() + 8
        match = None
        while match is None:
            self.terminal.read()
            match = STATUS.search(self.terminal.raw)
            assert self.terminal.child.poll() is None and time.monotonic() < deadline, self.terminal.text()[-2000:]
        assert int(match[1]) == expected, (expected, match[1], self.terminal.text()[-2000:])
        restored, initial = termios.tcgetattr(self.terminal.slave), self.terminal.initial[:]
        if sys.platform == 'darwin':
            restored[3] &= ~termios.PENDIN
            initial[3] &= ~termios.PENDIN
        assert restored == initial, 'terminal input mode was not restored'
        visible = self.visible()
        assert visible.count(ORIGINAL) == 1, ('original screen was not restored', visible)
        logical = visible.replace('\n', '')
        for value in identifiers:
            assert logical.count(value) == 1, ('permanent ID missing or duplicated', value, visible)
        assert all(value not in visible for value in ('Space: more', 'b: back', 'q: quit', 'i: inline')), ('final controls remain', visible)
        if self.expect_viewer and expected in (0, 130, 143):
            assert 'file-demo-' not in visible and 'model-demo-' not in visible, ('pager dumped records into history', visible)
        alternate, entered = False, False
        for control in re.finditer(rb'\x1b\[\?(?:47|1047|1049)([hl])|\x1b\[([23])J', self.terminal.raw):
            mode, erase = control.groups()
            if mode:
                alternate = mode == b'h'
                entered = entered or alternate
            else:
                assert erase == b'2' and alternate, 'original screen or history cleared'
        assert entered == self.expect_viewer and not alternate, 'alternate screen policy or restoration failed'
        for mode in (b'25', b'2004'):
            on = self.terminal.raw.rfind(b'\x1b[?' + mode + b'h')
            off = self.terminal.raw.rfind(b'\x1b[?' + mode + b'l')
            if mode == b'25' and off >= 0:
                assert on > off, 'cursor remained hidden'
            if mode == b'2004' and on >= 0:
                assert off > on, 'paste remained enabled'
        self.send('\n')
        while self.terminal.child.poll() is None:
            self.terminal.read()
            assert time.monotonic() < deadline, 'observer shell did not exit'
        drain(self.terminal)
        assert self.terminal.child.returncode == expected
        self.final_text = visible
        return visible

    def close(self):
        self.terminal.save(self.output / 'terminal')
        (self.output / 'requests.json').write_text(json.dumps(requests(self.server), indent=2) + '\n')
        (self.output / 'keys.json').write_text(json.dumps(self.keys) + '\n')
        self.terminal.close()
        self.home.cleanup()


def run_case(name, binary, output, action, *, args=(), fixture_options=None, **session_options):
    with fixture.serve(**(fixture_options or {})) as server:
        session = Session(binary, output / name, server, args, **session_options)
        try:
            action(session, server)
            result = {'name': name, 'result': 'PASS', 'requests': requests(server),
                      'maximum_inflight': server.maximum_inflight, 'keys': session.keys}
            (session.output / 'result.json').write_text(json.dumps(result, indent=2) + '\n')
            print(f'PASS {name}', flush=True)
            return result
        finally:
            session.close()


def ready(s, page=1):
    prefix = 'model-demo-' if s.server.resource == 'models' else 'file-demo-'
    s.wait_visible(fixture.identifier(page, prefix=prefix))
    return s.wait_visible('q: quit')


def lazy_back(s, server):
    first = ready(s)
    assert 'i: inline' not in first
    s.count(1)
    s.send('i')
    s.count(1)
    assert s.visible() == first, 'i changed the pager'
    s.send(' ')
    ready(s, 2)
    s.count(2)
    s.send('b')
    ready(s, 1)
    s.count(2)
    s.send(' ')
    ready(s, 2)
    s.count(2)
    s.send(' ')
    ready(s, 3)
    s.count(3)
    s.send(' ')
    ready(s, 3)
    s.count(3)
    assert s.terminal.child.poll() is None, 'final page exited automatically'
    s.send('b')
    ready(s, 2)
    s.count(3)
    s.send('q')
    s.finish(0)


def short_page(s, server):
    ready(s)
    s.count(1)
    assert s.terminal.child.poll() is None, 'short page exited automatically'
    s.send('q')
    s.finish(0)


def empty_page(s, server):
    s.wait_visible('No results.')
    s.wait_visible('q: quit')
    s.count(1)
    s.send('q')
    s.finish(0)


def loaded_chunks(s, server):
    prefix = 'model-demo-' if server.resource == 'models' else 'file-demo-'
    first, last = [fixture.identifier(1, row, prefix=prefix) for row in (1, server.rows)]
    ready(s)
    s.send(' ')
    s.wait_visible(last)
    s.count(1)
    s.send('b')
    s.wait_visible(first, absent=last)
    s.count(1)
    s.send('i')
    s.count(1)
    s.wait_visible(first, absent=last)
    s.send('q')
    s.finish(0)


def narrow_resize(s, server):
    s.wait_visible('page-001-item-001.jsonl')
    s.wait_visible('q: quit')
    s.terminal.resize(20, 6)
    s.count(1)
    s.terminal.resize(40, 10)
    s.wait_visible('q: quit')
    s.count(1)
    s.send('q')
    s.finish(0)


def pending_cancel(s, server, key, status):
    ready(s)
    s.send(' ')
    deadline = time.monotonic() + 8
    while not server.blocked.is_set():
        s.terminal.read()
        assert time.monotonic() < deadline, 'second request never blocked'
    s.send('i')
    s.count(2)
    s.send(key)
    s.finish(status)
    assert server.disconnected.wait(3), 'request cancellation did not close the connection'
    s.count(2)


def request_error(s, server):
    if server.error_page == 2:
        ready(s)
        s.send(' ')
    text = s.finish(1, [fixture.identifier(1)] if server.error_page == 2 else [])
    assert 'HTTP 400' in text, text
    if server.error_page == 1:
        assert 'file-demo-' not in text, text
    s.count(server.error_page)


def noninteractive(s, server):
    s.finish(0, [fixture.identifier(page) for page in (1, 2, 3)])
    s.count(3)
    assert s.keys == ['\n'], s.keys


def external_signal(s, server, name, status):
    ready(s)
    s.count(1)
    identities = re.findall(rb'__VIEWER_PID_([0-9]+)__', s.terminal.raw)
    assert len(identities) == 1, ('ambiguous owned CLI', identities)
    pid = int(identities[0])
    assert pid != s.terminal.child.pid, 'signal target is the observer shell'
    (s.output / 'signal.json').write_text(json.dumps({'signal': name, 'pid': pid,
        'parent_pid': s.terminal.child.pid, 'executable': str(s.binary), 'expected_status': status}, indent=2) + '\n')
    os.kill(pid, getattr(signal, name))
    s.finish(status)
    s.count(1)


def pipe_checks(binary, baseline, output):
    checks = []
    with tempfile.TemporaryDirectory(prefix='pager-pipes-') as home:
        for mode, extra in [('text', []), ('json', ['--format', 'json'])]:
            results = []
            for label, candidate in [('before', baseline), ('after', binary)]:
                with fixture.serve() as server:
                    env = {'PATH': '/usr/bin:/bin', 'HOME': home, 'XDG_CONFIG_HOME': home,
                           'OPENAI_API_KEY': 'synthetic-pager-key', 'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1',
                           'OPENAI_CLI_NO_UPDATE_CHECK': '1', 'OPENAI_CLI_NO_AUTO_UPDATE': '1', 'NO_COLOR': '1', 'TERM': 'xterm-256color'}
                    result = subprocess.run([str(candidate), *extra, 'files', 'list', '--limit', '1', '--max-items', '-1'],
                                            env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=10)
                    assert result.returncode == 0 and len(requests(server)) == 3, (result.stderr, requests(server))
                    (output / f'pipe-{mode}-{label}.stdout').write_bytes(result.stdout)
                    (output / f'pipe-{mode}-{label}.stderr').write_bytes(result.stderr)
                    results.append((result.stdout, result.stderr, result.returncode))
            assert results[0] == results[1], (mode, 'pipe output changed')
            checks.append({'name': 'pipe-' + mode, 'result': 'PASS'})
        for flag in ('--pager', '--no-pager'):
            with fixture.serve() as server:
                env['OPENAI_BASE_URL'] = f'http://127.0.0.1:{server.server_port}/v1'
                result = subprocess.run([str(binary), 'files', 'list', flag], env=env, stdin=subprocess.DEVNULL,
                                        capture_output=True, timeout=10)
                assert result.returncode != 0 and requests(server) == [], (result, requests(server))
                (output / (flag[2:] + '.stderr')).write_bytes(result.stderr)
                checks.append({'name': 'removed-' + flag[2:], 'result': 'PASS', 'status': result.returncode})
        for command in (['files', 'list'], ['batches', 'list'], ['admin', 'organization', 'projects', 'list'], ['models', 'list']):
            result = subprocess.run([str(binary), *command, '--help'], env=env, stdin=subprocess.DEVNULL,
                                    capture_output=True, timeout=10)
            assert result.returncode == 0 and b'--pager' not in result.stdout and b'--no-pager' not in result.stdout, (command, result)
            assert b'Space' in result.stdout and b'back' in result.stdout, (command, result.stdout)
            (output / ('help-' + command[0] + '.txt')).write_bytes(result.stdout)
            checks.append({'name': 'help-' + command[0], 'result': 'PASS'})
    print('PASS pipe output, removed flags, and executable help', flush=True)
    return checks


def main():
    global ASCIINEMA
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--baseline', type=Path, required=True)
    parser.add_argument('--asciinema', default='asciinema')
    parser.add_argument('--case', action='append', default=[])
    args = parser.parse_args()
    ASCIINEMA = args.asciinema
    args.output.mkdir()
    for name in ('observe.py', 'fixture.py'):
        (args.output / ('source-' + name)).write_bytes((HERE / name).read_bytes())
    binary = args.binary.resolve()
    binary_hash = hashlib.sha256(binary.read_bytes()).hexdigest()
    baseline_hash = hashlib.sha256(args.baseline.read_bytes()).hexdigest()
    (args.output / 'binary.json').write_text(json.dumps({'path': str(binary), 'sha256': binary_hash,
        'baseline': str(args.baseline.resolve()), 'baseline_sha256': baseline_hash}, indent=2) + '\n')
    cases = [
        ('lazy-back-and-ignored-i', lazy_back, {}),
        ('files-short-final', short_page, {'fixture_options': {'pages': 1}}),
        ('batches-short-final', short_page, {'fixture_options': {'pages': 1, 'resource': 'batches'}}),
        ('projects-short-final', short_page, {'fixture_options': {'pages': 1, 'resource': 'projects'}}),
        ('models-short-final', short_page, {'fixture_options': {'pages': 1, 'resource': 'models'}}),
        ('empty-final', empty_page, {'fixture_options': {'pages': 1, 'rows': 0}}),
        ('loaded-chunks', loaded_chunks, {'fixture_options': {'pages': 1, 'rows': 8}, 'height': 8}),
        ('models-loaded-chunks', loaded_chunks, {'fixture_options': {'pages': 1, 'rows': 8, 'resource': 'models'}, 'height': 8}),
        ('narrow-resize', narrow_resize, {'fixture_options': {'pages': 1, 'rows': 2, 'id_length': 101}, 'width': 40, 'height': 10}),
        ('pending-q', lambda s, server: pending_cancel(s, server, 'q', 0), {'fixture_options': {'block_page': 2}}),
        ('pending-ctrl-c', lambda s, server: pending_cancel(s, server, '\x03', 130), {'fixture_options': {'block_page': 2}}),
        ('first-error', request_error, {'fixture_options': {'error_page': 1}}),
        ('next-error', request_error, {'fixture_options': {'error_page': 2}}),
        ('json-format', noninteractive, {'args': ('--format', 'json'), 'extra_env': {'PAGER': 'cat'}}),
        ('text-format', noninteractive, {'args': ('--format', 'text')}),
        ('ci-override', noninteractive, {'extra_env': {'CI': '1'}}),
        ('dumb-terminal', noninteractive, {'extra_env': {'TERM': 'dumb'}}),
        ('files-sigint', lambda s, server: external_signal(s, server, 'SIGINT', 130), {'pid_handshake': True}),
        ('files-sigterm', lambda s, server: external_signal(s, server, 'SIGTERM', 143), {'pid_handshake': True}),
        ('models-sigint', lambda s, server: external_signal(s, server, 'SIGINT', 130), {'pid_handshake': True, 'fixture_options': {'resource': 'models', 'pages': 1}}),
        ('models-sigterm', lambda s, server: external_signal(s, server, 'SIGTERM', 0), {'pid_handshake': True, 'fixture_options': {'resource': 'models', 'pages': 1}}),
    ]
    available = {name for name, _, _ in cases} | {'pipe-policy'}
    assert set(args.case) <= available, ('unknown cases', set(args.case) - available)
    checks = []
    for name, action, options in cases:
        if not args.case or name in args.case:
            checks.append(run_case(name, binary, args.output, action, **options))
    if not args.case or 'pipe-policy' in args.case:
        checks.extend(pipe_checks(binary, args.baseline, args.output))
    assert hashlib.sha256(binary.read_bytes()).hexdigest() == binary_hash, 'binary changed during verification'
    assert hashlib.sha256(args.baseline.read_bytes()).hexdigest() == baseline_hash, 'baseline changed during verification'
    report = {'binary': str(binary), 'sha256': binary_hash, 'baseline_sha256': baseline_hash,
              'checks': checks, 'count': len(checks), 'scope': 'Synthetic private PTYs; native Terminal appearance remains unverified.'}
    (args.output / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f'PASS {len(checks)} pager checks')


if __name__ == '__main__':
    main()
