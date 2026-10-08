#!/usr/bin/env python3
"""Check demand-driven lists through public commands and private PTYs.

Usage: python3 -I -B scripts/check-list-navigation.py BINARY OUTPUT [--case NAME]
Uses synthetic loopback responses, fake credentials, and temporary homes.
"""
import argparse
import contextlib
import hashlib
import http.server
import importlib.util
import json
import os
import pathlib
import platform
import re
import select
import socket
import subprocess
import sys
import tempfile
import termios
import threading
import time
import unicodedata
import urllib.parse


spec = importlib.util.spec_from_file_location(
    'navigation_terminal', pathlib.Path(__file__).with_name('image_picker_harness.py'))
terminal_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(terminal_module)

RESOURCES = {
    'files': (['files', 'list'], '/files', 'file'),
    'batches': (['batches', 'list'], '/batches', 'batch'),
    'projects': (['admin', 'organization', 'projects', 'list'], '/organization/projects', 'organization.project'),
    'models': (['models', 'list'], '/models', 'model'),
    'vector-stores': (['vector-stores', 'list'], '/vector_stores', 'vector_store'),
}
FOOTER = 'Space: more   b: back   q: quit'
PRINT_HINT = 'p: print page, quit'
ASCIINEMA = 'asciinema'
TABLE_HEADERS = {
    'files': ['ID', 'FILENAME', 'PURPOSE', 'SIZE', 'STATUS'],
    'batches': ['ID', 'STATUS'],
    'projects': ['ID', 'NAME', 'STATUS'],
}


def item_id(number, length=None):
    if length is None:
        return f'item_{number:03d}'
    suffix = f'_{number:03d}'
    return ('item_'+'synthetic'*length)[:length-len(suffix)]+suffix


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        parsed = urllib.parse.urlsplit(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        cursor = query.get('after', [''])[0]
        with self.server.lock:
            self.server.requests.append({'path': parsed.path, 'query': query})
            self.server.inflight += 1
            self.server.maximum_inflight = max(self.server.maximum_inflight, self.server.inflight)
        try:
            assert parsed.path == self.server.path, parsed.path
            if self.server.path != '/models':
                assert query.get('limit') == [str(self.server.rows)], query
            second = bool(cursor)
            if second:
                assert cursor == item_id(self.server.rows, self.server.id_length), query
            if ((second and self.server.mode == 'blocked') or
                    (not second and self.server.mode == 'blocked-first')):
                while not self.server.release.wait(0.02):
                    if select.select([self.connection], [], [], 0)[0]:
                        if not self.connection.recv(1, socket.MSG_PEEK):
                            self.server.disconnected.set()
                            return
            if (second and self.server.mode == 'error') or self.server.mode == 'error-first':
                status, value = 400, {'error': {'message': 'synthetic page failure', 'type': 'invalid_request_error'}}
            else:
                status = 200
                first = 1 if not cursor else int(cursor.rsplit('_', 1)[1]) + 1
                if self.server.mode == 'stalled' and second:
                    first = 1
                items = [] if self.server.mode.startswith('empty') else [
                    {'id': item_id(number, self.server.id_length), 'object': self.server.object,
                     'filename': f'synthetic-{number:03d}.txt', 'name': f'Synthetic {number:03d}',
                     'status': 'completed', 'purpose': 'assistants', 'bytes': 5}
                    for number in range(first, first + self.server.rows)]
                if self.server.id_length is not None:
                    for number, item in enumerate(items, start=first):
                        # Keep page markers intact at widths 20 and 40, even
                        # while the viewport wraps the long ID for display.
                        item.update(filename=f'p{number:03d}.txt', name=f'p{number:03d}',
                                    endpoint=f'/p{number:03d}')
                if self.server.item_profile:
                    fields = {
                        'file': ('id', 'object', 'filename', 'purpose', 'bytes', 'status'),
                        'batch': ('id', 'object', 'status'),
                        'organization.project': ('id', 'object', 'name', 'status'),
                    }[self.server.object]
                    items = [{key: item[key] for key in fields} for item in items]
                    if self.server.item_profile == 'unicode':
                        for item in items:
                            for key in ('filename', 'name'):
                                if key in item:
                                    item[key] = '界面-cafe\u0301-測試.txt'
                if self.server.tab_value is not None:
                    # Missing object metadata requires labeled fallback. Keep
                    # the payload minimal so two/three-row boundaries matter.
                    items = [{'id': item['id'], 'filename': self.server.tab_value} for item in items]
                more = self.server.mode in {'stalled', 'empty-more'} or not second
                if self.server.mode in {'empty-last', 'complete'} or self.server.path == '/models':
                    more = False
                value = {'object': 'list', 'data': items, 'has_more': more,
                         'first_id': items[0]['id'] if items else '',
                         'last_id': items[-1]['id'] if items else ''}
                if self.server.mode in {'unknown-more', 'empty-unknown'}:
                    del value['has_more']
            body = json.dumps(value).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            self.server.disconnected.set()
        except Exception as error:
            with self.server.lock:
                self.server.errors.append(str(error))
        finally:
            with self.server.lock:
                self.server.inflight -= 1


def drain(terminal, duration=0.25):
    deadline = time.monotonic() + duration
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline-time.monotonic())))


def reconstruct_text(events, width, height):
    with tempfile.TemporaryDirectory(prefix='list-navigation-screen-') as temporary:
        recording = pathlib.Path(temporary)/'screen.cast'
        header = {'version': 2, 'width': width, 'height': height}
        recording.write_text('\n'.join(json.dumps(value) for value in [header, *events])+'\n')
        result = subprocess.run([ASCIINEMA, 'convert', '-f', 'txt', str(recording), '-'],
                                capture_output=True, text=True, check=True, timeout=5,
                                env={'PATH': os.environ.get('PATH', '/usr/bin:/bin'),
                                     'HOME': temporary, 'ASCIINEMA_STATE_HOME': temporary+'/state',
                                     'ASCIINEMA_CONFIG_HOME': temporary+'/config'})
    return result.stdout


def screen_text(terminal):
    # Tea emits cursor-addressed differences. Stripping ANSI cannot reconstruct
    # the visible value (item_001 can become item_002 by writing only "2").
    # Reuse asciinema's terminal emulator, already required by our demo tooling.
    cache_key = (len(terminal.raw), len(terminal.events))
    if getattr(terminal, '_screen_key', None) == cache_key:
        return terminal._screen_text
    result = reconstruct_text(terminal.events, *terminal.initial_size)
    terminal._screen_key, terminal._screen_text = cache_key, result
    return result


def assert_printed_page(terminal, expected_ids, width, destination):
    # Only bytes after terminal restoration can satisfy this assertion.
    # Earlier viewport frames must not hide a missing or duplicated print.
    marker = b'\x1b[?2004l'
    assert marker in terminal.raw, 'print did not restore terminal input'
    printed = terminal.raw.rsplit(marker, 1)[-1]
    events = [[0, 'o', printed.decode('utf-8', 'replace')]]
    header = {'version': 2, 'width': width, 'height': 30}
    destination.with_suffix('.printed.raw').write_bytes(printed)
    destination.with_suffix('.printed.cast').write_text('\n'.join(
        json.dumps(value) for value in [header, *events])+'\n')
    text = reconstruct_text(events, width, 30)
    destination.with_suffix('.printed.txt').write_text(text)
    identifiers = re.findall(r'^ID: (\S+)$', text, re.MULTILINE)
    assert identifiers == expected_ids, ('printed page IDs changed', identifiers, expected_ids, text)
    for identifier in expected_ids:
        assert ('ID: '+identifier+'\r\n').encode() in printed, 'print inserted a newline inside the ID'
    assert PRINT_HINT not in text and FOOTER not in text, 'print tail contains another viewer frame'


def assert_current_page(terminal, number, destination):
    wait_screen(terminal, f'p{number:03d}')
    drain(terminal)
    text = screen_text(terminal)
    destination.write_text(text)
    assert f'p{3-number:03d}' not in text, ('stale page remained visible', text)
    assert PRINT_HINT in text, ('print hint missing', text)


def wait_screen(terminal, marker):
    deadline = time.monotonic()+12
    while marker not in screen_text(terminal):
        terminal.read()
        assert terminal.child.poll() is None, terminal.text()[-1500:]
        assert time.monotonic() < deadline, ('screen marker missing', marker, screen_text(terminal)[-1500:])


def finish(terminal, expected):
    deadline = time.monotonic() + 6
    while terminal.child.poll() is None:
        terminal.read()
        assert time.monotonic() < deadline, 'command did not exit: ' + terminal.text()[-1500:]
    drain(terminal, 0.1)
    assert terminal.child.returncode == expected, (terminal.child.returncode, expected, terminal.text()[-2000:])
    restored = termios.tcgetattr(terminal.slave)
    initial = terminal.initial[:]
    if sys.platform == 'darwin':
        restored[3] &= ~termios.PENDIN
        initial[3] &= ~termios.PENDIN
    assert restored == initial, 'terminal input mode was not restored'
    for mode in (b'25', b'2004'):
        on = terminal.raw.rfind(b'\x1b[?' + mode + b'h')
        off = terminal.raw.rfind(b'\x1b[?' + mode + b'l')
        if mode == b'25' and off >= 0:
            assert on > off, 'cursor remained hidden'
        if mode == b'2004' and on >= 0:
            assert off > on, 'bracketed paste remained enabled'
    assert not re.search(rb'\x1b\[\?(?:47|1047|1049)[hl]', terminal.raw), 'navigation used an alternate screen'


def request_count(server):
    with server.lock:
        return len(server.requests)


def wait_requests(terminal, server, count):
    deadline = time.monotonic() + 6
    while request_count(server) < count:
        terminal.read()
        assert terminal.child.poll() is None, terminal.text()[-1500:]
        assert time.monotonic() < deadline, ('request did not arrive', count, terminal.text()[-1500:])


def ready(terminal, server):
    terminal.wait('item_001')
    terminal.wait(FOOTER)
    drain(terminal)
    assert request_count(server) == 1, 'idle navigation fetched another page'
    assert terminal.child.poll() is None, 'navigation did not wait for input'


def quit_navigation(terminal):
    terminal.send(b'q')
    finish(terminal, 0)


def navigation_footer(width, complete=False):
    if complete:
        footer = 'End of results   b: back   q: quit'
        if len(footer) > width:
            footer = 'End of results   q: quit'
        if len(footer) > width:
            footer = 'End q: quit'
    else:
        footer = FOOTER if len(FOOTER) <= width else 'Space: more   q: quit'
    return footer.replace('   ', ' ') if width < 21 else footer


def assert_complete_scroll_controls(terminal, width):
    # A complete API response can still contain unread screen rows. End belongs
    # only at the bottom; backward navigation must restore the more/back hint.
    wait_screen(terminal, navigation_footer(width))
    assert 'End of results' not in screen_text(terminal) and 'End q: quit' not in screen_text(terminal)
    terminal.send(b' ')
    wait_screen(terminal, navigation_footer(width, complete=True))
    terminal.send(b'b')
    wait_screen(terminal, navigation_footer(width))


def completed_output(terminal, expected_ids):
    """Require an automatic exit, one final result, and restored terminal modes."""
    finish(terminal, 0)
    restored = b'\x1b[?2004l'
    printed = bytes(terminal.raw).rsplit(restored, 1)[-1]
    text = terminal_module.ANSI.sub('', printed.decode('utf-8')).replace('\r\n', '\n').replace('\r', '')
    assert 'q: quit' not in text, ('viewer footer in completed output', text)
    assert 'p: print' not in text and 'p: all' not in text, ('viewer print hint in completed output', text)
    assert 'End of results' not in text and 'Loading next page' not in text, text
    for identifier in expected_ids:
        assert text.count(identifier) == 1, ('missing or duplicated completed item', identifier, text)
        assert terminal.raw.count(identifier.encode()) == 1, ('result rendered before final print', identifier)
    return text


def physical_rows(text, width):
    # Fixtures use ordinary characters, CJK, combining accents, and default
    # eight-column tab stops. A tab clamps at the last column and clears pending
    # wrap; it does not behave like enough spaces to cross the right margin.
    total = 0
    for line in text.rstrip('\n').split('\n') if text else []:
        rows, column = 1, 0
        for char in line:
            if char == '\t':
                column = min(width-1, (min(column, width-1)//8+1)*8)
                continue
            cells = 0 if unicodedata.combining(char) else 2 if unicodedata.east_asian_width(char) in {'W', 'F'} else 1
            if cells and column+cells > width:
                rows, column = rows+1, 0
            column += cells
        total += rows
    return total


def main():
    global ASCIINEMA
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    parser.add_argument('--case', action='append', dest='cases')
    parser.add_argument('--suite', choices=('all', 'legacy', 'short'), default='all',
                        help='Select preserved navigation checks or complete-one-screen checks')
    parser.add_argument('--asciinema', default='asciinema', help='Existing asciinema executable for screen assertions')
    parser.add_argument('--entrypoint', choices=('public', 'helper'), default='public',
                        help='Label helper-only checks without claiming public-command evidence')
    args = parser.parse_args()
    ASCIINEMA = args.asciinema
    binary = pathlib.Path(args.binary).resolve()
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    (output/'results.json').unlink(missing_ok=True)
    results = []
    selected = set(args.cases or [])

    @contextlib.contextmanager
    def case(name, resource='files', mode='normal', rows=1, flags=(), extra=(), width=80, height=24,
             stderr=False, env_extra=None, stdin_pipe=False, stdout_pipe=False, id_length=None,
             item_profile=None, tab_value=None):
        with tempfile.TemporaryDirectory(prefix='list-navigation-') as temporary:
            command, path, object_name = RESOURCES[resource]
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
            server.path, server.object, server.mode, server.rows = path, object_name, mode, rows
            server.id_length = id_length
            server.item_profile = item_profile
            server.tab_value = tab_value
            server.requests, server.errors = [], []
            server.inflight = server.maximum_inflight = 0
            server.lock = threading.Lock()
            server.release, server.disconnected = threading.Event(), threading.Event()
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            env = {'PATH': '/usr/bin:/bin', 'HOME': temporary, 'TERM': 'xterm-256color',
                   'LANG': 'en_US.UTF-8', 'FORCE_COLOR': '0', 'NO_COLOR': '1',
                   'GOMAXPROCS': '2',
                   'OPENAI_API_KEY': 'sk-fake-navigation-test', 'OPENAI_ADMIN_KEY': 'sk-fake-navigation-admin',
                   'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}'}
            env.update(env_extra or {})
            terminal = None
            diagnostic = open(output/(name+'.stderr'), 'wb') if stderr else None
            input_file = open(os.devnull, 'rb') if stdin_pipe else None
            output_file = open(output/(name+'.stdout'), 'wb') if stdout_pipe else None
            try:
                limit_flags = [] if resource == 'models' else ['--limit', str(rows)]
                terminal = terminal_module.Terminal(str(binary), [*flags, *command, *limit_flags, *extra],
                                                    env, width=width, height=height, stderr_file=diagnostic,
                                                    input_file=input_file, output_file=output_file)
                yield terminal, server
                assert not server.errors, server.errors
                results.append({'case': name, 'passed': True, 'requests': request_count(server),
                                'maximum_inflight': server.maximum_inflight,
                                'exit': terminal.child.returncode})
                print('PASS', name, flush=True)
            finally:
                try:
                    if terminal is not None:
                        (output/(name+'.raw')).write_bytes(terminal.raw)
                        header = {'version': 2, 'width': width, 'height': height,
                                  'title': 'List navigation; synthetic loopback API'}
                        (output/(name+'.cast')).write_text('\n'.join(
                            json.dumps(value) for value in [header, *terminal.events])+'\n')
                        terminal.close()
                    if diagnostic is not None:
                        diagnostic.close()
                    if input_file is not None:
                        input_file.close()
                    if output_file is not None:
                        output_file.close()
                finally:
                    server.release.set()
                    server.shutdown()
                    server.server_close()
                    worker.join()
                    (output/(name+'.requests.json')).write_text(json.dumps(server.requests, indent=2)+'\n')

    def enabled(name):
        if selected:
            return name in selected
        return args.suite == 'all' or (name.startswith('short-') == (args.suite == 'short'))

    for resource in ('files', 'batches', 'projects'):
        for width in (20, 40):
            name = f'{resource}-long-id-print-{width}'
            if enabled(name):
                with case(name, resource=resource, width=width, height=30, id_length=77,
                          extra=('--max-items', '-1')) as (terminal, server):
                    assert_current_page(terminal, 1, output/(name+'.first.txt'))
                    assert request_count(server) == 1, 'idle navigation fetched another page'
                    assert terminal.child.poll() is None, 'navigation did not wait for input'
                    for resized_width in (60-width, width):
                        terminal.resize(resized_width, 30)
                        drain(terminal)
                        assert_current_page(terminal, 1, output/(name+f'.resize-{resized_width}.txt'))
                        assert request_count(server) == 1, 'resize fetched another page'
                    terminal.send(b' ')
                    assert_current_page(terminal, 2, output/(name+'.second.txt'))
                    assert request_count(server) == 2
                    assert server.requests[1]['query']['after'] == [item_id(1, 77)]
                    terminal.send(b'b')
                    assert_current_page(terminal, 1, output/(name+'.revisit.txt'))
                    terminal.send(b' ')
                    assert_current_page(terminal, 2, output/(name+'.revisit-second.txt'))
                    assert request_count(server) == 2, 'loaded-page navigation fetched another page'
                    terminal.send(b'p')
                    finish(terminal, 0)
                    assert request_count(server) == 2 and server.maximum_inflight == 1
                    assert_printed_page(terminal, [item_id(2, 77)], width, output/name)

    for width in (20, 40):
        for length in (width-5, width-4, width-3):
            name = f'print-id-boundary-{width}-{length}'
            if enabled(name):
                with case(name, width=width, height=30, id_length=length) as (terminal, server):
                    assert_current_page(terminal, 1, output/(name+'.first.txt'))
                    terminal.send(b'p')
                    finish(terminal, 0)
                    assert request_count(server) == 1, 'printing fetched another page'
                    assert_printed_page(terminal, [item_id(1, length)], width, output/name)

    name = 'print-during-active-fetch'
    if enabled(name):
        with case(name, mode='blocked', width=20, height=30, id_length=77) as (terminal, server):
            assert_current_page(terminal, 1, output/(name+'.first.txt'))
            terminal.send(b' ')
            wait_requests(terminal, server, 2)
            wait_screen(terminal, 'Loading')
            terminal.send(b'p')
            finish(terminal, 0)
            assert server.disconnected.wait(2), 'p did not cancel the active request'
            assert request_count(server) == 2 and server.maximum_inflight == 1
            assert server.requests[1]['query']['after'] == [item_id(1, 77)]
            assert_printed_page(terminal, [item_id(1, 77)], 20, output/name)

    for resource in ('files', 'batches', 'projects'):
        for format_name, flags in [('default', ()), ('auto', ('--format', 'auto'))]:
            name = resource+'-'+format_name+'-idle'
            if enabled(name):
                with case(name, resource=resource, flags=flags) as (terminal, server):
                    ready(terminal, server)
                    terminal.resize(54, 18)
                    drain(terminal)
                    assert request_count(server) == 1, 'resize fetched another page'
                    quit_navigation(terminal)
                    assert request_count(server) == 1
        name = resource+'-text-bypass'
        if enabled(name):
            with case(name, resource=resource, flags=('--format', 'text'), height=120) as (terminal, server):
                finish(terminal, 0)
                assert request_count(server) == 2 and FOOTER not in terminal.text()
                assert 'item_001' in terminal.text() and 'item_002' in terminal.text()

    for name, flags, calls in [
        ('json-bypass', ('--format', 'json'), 2),
        ('jsonl-bypass', ('--format', 'jsonl'), 2),
        ('raw-bypass', ('--format', 'raw'), 1),
        ('extraction-bypass', ('--transform', 'id'), 2),
        ('raw-extraction-bypass', ('--transform', 'id', '--raw-output'), 2),
        ('raw-output-bypass', ('--raw-output',), 2),
    ]:
        if enabled(name):
            with case(name, flags=flags, height=120) as (terminal, server):
                finish(terminal, 0)
                assert request_count(server) == calls
                assert FOOTER not in terminal.text() and 'Loading next page' not in terminal.text()
                assert 'item_001' in terminal.text()
                assert ('item_002' in terminal.text()) == (calls == 2)

    for name, options in [
        ('ci-bypass', {'env_extra': {'CI': 'true'}}),
        ('dumb-bypass', {'env_extra': {'TERM': 'dumb'}}),
        ('stdin-pipe-bypass', {'stdin_pipe': True}),
        ('stdout-pipe-bypass', {'stdout_pipe': True}),
    ]:
        if enabled(name):
            with case(name, **options) as (terminal, server):
                finish(terminal, 0)
                assert request_count(server) == 2
                assert FOOTER not in terminal.text()
            if name == 'stdout-pipe-bypass':
                data = (output/(name+'.stdout')).read_bytes()
                assert b'item_001' in data and b'item_002' in data
                assert b'\x1b' not in data and FOOTER.encode() not in data

    name = 'models-single-response'
    if enabled(name):
        with case(name, resource='models') as (terminal, server):
            text = completed_output(terminal, [item_id(1)])
            assert request_count(server) == 1
            assert text.startswith('ID\nitem_001\n') and 'Listed 1 model.' in text
            assert not any(label in text for label in ('ID:', 'Filename:', 'Status:', 'Owned by:'))

    name = 'vector-stores-default-bypass'
    if enabled(name):
        with case(name, resource='vector-stores', height=120) as (terminal, server):
            finish(terminal, 0)
            assert request_count(server) == 2, 'another pageable resource changed its fetch behavior'
            assert server.requests[1]['query']['after'] == ['item_001']
            assert 'item_001' in terminal.text() and 'item_002' in terminal.text()
            assert FOOTER not in terminal.text() and PRINT_HINT not in terminal.text()
            assert 'Loading next page' not in terminal.text()

    name = 'loaded-rows-before-fetch'
    if enabled(name):
        with case(name, rows=12, height=10, mode='blocked') as (terminal, server):
            ready(terminal, server)
            mark = len(terminal.raw)
            terminal.send(b' ')
            drain(terminal)
            assert len(terminal.raw) > mark, 'Space did not advance loaded rows'
            assert request_count(server) == 1, 'Space fetched before exhausting loaded rows'
            for _ in range(30):
                if request_count(server) == 2:
                    break
                terminal.send(b' ')
                drain(terminal, 0.08)
            wait_requests(terminal, server, 2)
            assert server.maximum_inflight == 1
            quit_navigation(terminal)
            assert server.disconnected.wait(2), 'q did not cancel the active request'

    name = 'repeated-space-and-active-cancel'
    if enabled(name):
        with case(name, mode='blocked') as (terminal, server):
            ready(terminal, server)
            terminal.send(b' ')
            wait_requests(terminal, server, 2)
            terminal.wait('Loading next page')
            terminal.send(b' '*40)
            drain(terminal, 0.4)
            assert request_count(server) == 2 and server.maximum_inflight == 1, 'concurrent page requests'
            quit_navigation(terminal)
            assert server.disconnected.wait(2), 'q did not cancel the active request'
            assert request_count(server) == 2

    name = 'first-request-cancel'
    if enabled(name):
        with case(name, mode='blocked-first') as (terminal, server):
            wait_requests(terminal, server, 1)
            terminal.wait('Loading next page')
            quit_navigation(terminal)
            assert server.disconnected.wait(2), 'q did not cancel the first request'
            assert request_count(server) == 1

    name = 'last-page'
    if enabled(name):
        with case(name) as (terminal, server):
            ready(terminal, server)
            terminal.send(b' ')
            wait_screen(terminal, 'item_002')
            terminal.wait('End of results')
            terminal.send(b'   ')
            drain(terminal)
            assert request_count(server) == 2
            quit_navigation(terminal)

    name = 'loaded-page-revisit'
    if enabled(name):
        with case(name) as (terminal, server):
            ready(terminal, server)
            terminal.send(b' ')
            wait_screen(terminal, 'item_002')
            terminal.send(b'b')
            wait_screen(terminal, 'item_001')
            terminal.send(b' ')
            wait_screen(terminal, 'item_002')
            assert request_count(server) == 2, 'revisiting loaded pages issued another request'
            quit_navigation(terminal)

    for mode in ('error', 'stalled', 'empty-more', 'empty-last'):
        name = mode
        if enabled(name):
            with case(name, mode=mode) as (terminal, server):
                if not mode.startswith('empty'):
                    ready(terminal, server)
                    terminal.send(b' ')
                if mode == 'empty-last':
                    completed_output(terminal, [])
                    assert request_count(server) == 1
                else:
                    finish(terminal, 1)
                    assert request_count(server) == (1 if mode == 'empty-more' else 2)
                    if not mode.startswith('empty'):
                        assert 'item_001' in terminal.text(), 'partial results disappeared'

    name = 'partial-json-error'
    if enabled(name):
        with case(name, mode='error', flags=('--format-error', 'json'), stderr=True) as (terminal, server):
            ready(terminal, server)
            terminal.send(b' ')
            finish(terminal, 1)
            assert request_count(server) == 2
        diagnostic = json.loads((output/(name+'.stderr')).read_bytes())
        assert diagnostic['message'] == 'synthetic page failure' and diagnostic['type'] == 'invalid_request_error'

    # Zero preserves the SDK iterator's first request, including API errors.
    for maximum, calls in [('0', 1), ('1', 1), ('2', 1), ('3', 2), ('-1', 2)]:
        name = 'max-items-'+maximum
        if enabled(name):
            with case(name, rows=2, extra=('--max-items', maximum)) as (terminal, server):
                if maximum == '0':
                    finish(terminal, 0)
                elif maximum in {'1', '2'}:
                    completed_output(terminal, [item_id(number) for number in range(1, int(maximum)+1)])
                else:
                    terminal.wait('item_001')
                    if calls == 2:
                        terminal.wait(FOOTER)
                        terminal.send(b' ')
                        wait_screen(terminal, 'item_003')
                    terminal.wait('End of results')
                    quit_navigation(terminal)
                assert request_count(server) == calls
                if maximum in {'1', '3'}:
                    assert f'item_{int(maximum)+1:03d}' not in screen_text(terminal), 'total item limit was exceeded'

    for resource in ('files', 'batches', 'projects'):
        for mode in ('complete', 'empty-last', 'empty-unknown'):
            name = f'short-{resource}-{mode}'
            if enabled(name):
                with case(name, resource=resource, mode=mode, width=110, item_profile='table') as (terminal, server):
                    text = completed_output(terminal, [item_id(1)] if mode == 'complete' else [])
                    (output/(name+'.txt')).write_text(text)
                    assert request_count(server) == 1 and server.maximum_inflight == 1
                    if mode.startswith('empty'):
                        assert text == 'No results.\n', ('empty result message changed', text)
                        assert not re.search(r'item_\d+', text), text
                    else:
                        assert text.splitlines()[0].split() == TABLE_HEADERS[resource], 'complete result lost its table'

        name = f'short-{resource}-request-cap'
        if enabled(name):
            with case(name, resource=resource, rows=2, width=110, item_profile='table',
                      extra=('--max-items', '1')) as (terminal, server):
                text = completed_output(terminal, [item_id(1)])
                assert item_id(2) not in text
                assert request_count(server) == 1

        name = f'short-{resource}-unknown-more'
        if enabled(name):
            with case(name, resource=resource, mode='unknown-more', width=110,
                      item_profile='table', extra=('--max-items', '-1')) as (terminal, server):
                ready(terminal, server)
                quit_navigation(terminal)
                assert request_count(server) == 1

        name = f'short-{resource}-later-final-page'
        if enabled(name):
            with case(name, resource=resource, width=110, item_profile='table',
                      extra=('--max-items', '-1')) as (terminal, server):
                ready(terminal, server)
                terminal.send(b' ')
                wait_screen(terminal, item_id(2))
                terminal.wait('End of results')
                drain(terminal)
                assert terminal.child.poll() is None, 'later final page exited without allowing revisit'
                terminal.send(b'b')
                wait_screen(terminal, item_id(1))
                assert request_count(server) == 2
                quit_navigation(terminal)

        for flags in ((), ('--format-error', 'json')):
            name = f'short-{resource}-max-zero-error'+('-json' if flags else '-text')
            if enabled(name):
                with case(name, resource=resource, mode='error-first', flags=flags, stderr=True,
                          item_profile='table', extra=('--max-items', '0')) as (terminal, server):
                    finish(terminal, 1)
                    assert request_count(server) == 1
                    assert not terminal.raw, 'zero item limit printed results or entered navigation'
                    diagnostic = (output/(name+'.stderr')).read_text()
                    if flags:
                        assert json.loads(diagnostic)['message'] == 'synthetic page failure'
                    else:
                        assert 'Request failed (400 Bad Request).' in diagnostic
                        assert 'For API error details, add --format-error json.' in diagnostic

        for profile, width, id_length in [('table', 110, None), ('unicode', 20, None), ('long-id', 40, 77)]:
            names = [f'short-{resource}-{profile}-{suffix}' for suffix in ('reference', 'exact-height', 'one-over')]
            if not any(enabled(name) for name in names):
                continue
            options = dict(resource=resource, mode='complete', rows=2, width=width,
                           item_profile='unicode' if profile == 'unicode' else 'table', id_length=id_length)
            identifiers = [item_id(number, id_length) for number in (1, 2)]
            with case(names[0], height=240, **options) as (terminal, server):
                reference = completed_output(terminal, identifiers)
                assert request_count(server) == 1
                (output/(names[0]+'.txt')).write_text(reference)
            rows_needed = physical_rows(reference, width)
            assert rows_needed > 2, ('fixture did not exercise a useful height boundary', reference)
            if profile == 'table':
                assert reference.splitlines()[0].split() == TABLE_HEADERS[resource], 'table boundary used fallback labels'
            if profile == 'unicode' and resource != 'batches':
                assert '界面' in reference and 'cafe\u0301' in reference, 'Unicode fixture was lost'
            with case(names[1], height=rows_needed+1, **options) as (terminal, server):
                actual = completed_output(terminal, identifiers)
                assert actual == reference, ('exact-fit result changed', actual, reference)
                assert request_count(server) == 1
            with case(names[2], height=rows_needed, **options) as (terminal, server):
                assert_complete_scroll_controls(terminal, width)
                drain(terminal)
                assert terminal.child.poll() is None, 'one-row overflow exited automatically'
                assert request_count(server) == 1
                terminal.resize(width, rows_needed+20)
                drain(terminal)
                assert terminal.child.poll() is None, 'resize unexpectedly dismissed navigation'
                assert request_count(server) == 1, 'resize fetched another page'
                terminal.send(b'p')
                finish(terminal, 0)
                assert_printed_page(terminal, identifiers, width, output/names[2])

    # Literal row counts make the two regression directions explicit. These
    # cases preserve HT bytes in final output rather than expanding tabs.
    for suffix, value, height, rows_needed, exits in [
        ('expands-overflow', 'a\t123456789', 3, 3, False),
        ('expands-fits', 'a\t123456789', 4, 3, True),
        ('short-fits', 'a\t1', 3, 2, True),
        ('cancels-wrap', 'abcdefghij\tZ', 3, 2, True),
        ('after-wide-overflow', '界\t12345', 3, 3, False),
        ('after-combining-overflow', 'e\u0301\t12345', 3, 3, False),
        ('after-wrapped-fits', 'abcdefghijklmnopqrst\tZ', 4, 3, True),
        ('trailing-fits', 'abcdefghij\t', 3, 2, True),
    ]:
        for resource in ('files', 'models'):
            name = f'short-{resource}-tab-'+suffix
            if not enabled(name):
                continue
            with case(name, resource=resource, mode='complete', width=20, height=height, tab_value=value) as (terminal, server):
                if exits:
                    text = completed_output(terminal, [item_id(1)])
                else:
                    assert_complete_scroll_controls(terminal, 20)
                    drain(terminal)
                    assert terminal.child.poll() is None, 'tab-expanded output exceeded the available rows but exited'
                    assert request_count(server) == 1
                    terminal.send(b'p')
                    finish(terminal, 0)
                    assert_printed_page(terminal, [item_id(1)], 20, output/name)
                    printed = bytes(terminal.raw).rsplit(b'\x1b[?2004l', 1)[-1]
                    text = terminal_module.ANSI.sub('', printed.decode('utf-8')).replace('\r\n', '\n').replace('\r', '')
                expected = f'ID: {item_id(1)}\nFilename: {value}\n'
                assert text == expected, ('tab bytes or final result changed', text, expected)
                assert physical_rows(text, 20) == rows_needed, ('fixture terminal-row accounting changed', text, rows_needed)
                assert request_count(server) == 1 and server.maximum_inflight == 1
                (output/(name+'.txt')).write_text(text)

    unknown = selected-{result['case'] for result in results}
    assert not unknown, ('unknown cases', sorted(unknown))
    (output/'results.json').write_text(json.dumps({'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
        'platform': platform.platform(), 'entrypoint': args.entrypoint,
        'evidence': f'synthetic {args.entrypoint} entrypoints in private PTYs', 'cases': results}, indent=2)+'\n')


if __name__ == '__main__':
    main()
