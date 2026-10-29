#!/usr/bin/env python3
"""Capture actual synthetic Batch browser PTYs for local visual review."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import time
from http.server import HTTPServer

sys.dont_write_bytecode = True
REPO = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO/'scripts/demos/batches'))
from try_browser import BrowserFixture
spec = importlib.util.spec_from_file_location('browser_style_terminal', REPO/'scripts/image_picker_harness.py')
harness = importlib.util.module_from_spec(spec)
spec.loader.exec_module(harness)


def settle(terminal, duration=0.4):
    until = time.monotonic()+duration
    while time.monotonic() < until:
        terminal.read(min(0.03, max(0, until-time.monotonic())))


def action_index(screen, label):
    labels = {'View results', 'View errors', 'View input', 'Batch details', 'Save a file'}
    visible = [line.strip(' │>▸▾') for line in screen.splitlines()]
    actions = [name for line in visible for name in labels
               if line == name or line.startswith(name + '  ')]
    assert actions.count(label) == 1, ('action missing or duplicated', label, actions)
    return actions.index(label)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--width', type=int, default=90)
    parser.add_argument('--height', type=int, default=22)
    parser.add_argument('--color', choices=('dark', 'light', 'mono'), default='dark')
    parser.add_argument('--actions', action='store_true')
    content = parser.add_mutually_exclusive_group()
    content.add_argument('--message', choices=('pending','results','errors','input','details'))
    content.add_argument('--expand-save', action='store_true', help='capture the expanded Save a file action')
    content.add_argument('--save-path', action='store_true', help='capture an inline destination without saving a file')
    parser.add_argument('--query', default='')
    batch_choice = parser.add_mutually_exclusive_group()
    batch_choice.add_argument('--success', action='store_true')
    batch_choice.add_argument('--partial', action='store_true', help='select the batch with one failed request')
    parser.add_argument('--search-focus', action='store_true')
    parser.add_argument('--has-more', action='store_true')
    parser.add_argument('--select-rows', action='store_true')
    args = parser.parse_args()
    args.binary = args.binary.resolve()
    args.output.mkdir(parents=True, exist_ok=False)
    log = (args.output/'requests.jsonl').open('x')
    fixture = BrowserFixture(log)
    result = {'binary_sha256': hashlib.sha256(args.binary.read_bytes()).hexdigest(),
              'width': args.width, 'height': args.height, 'color': args.color,
              'source': 'actual macOS CLI PTY with synthetic loopback API', 'screens': {},
              'text_limitation': 'The text decoder is a limited test helper. Use rendered replays for font and terminal-cell spacing.'}
    with tempfile.TemporaryDirectory(prefix='f13-style-home-') as home:
        environment = {'PATH': os.defpath, 'HOME': home, 'XDG_CONFIG_HOME': home, 'XDG_CACHE_HOME': home,
                       'OPENAI_API_KEY': 'synthetic-demo-key', 'OPENAI_CLI_NO_UPDATE_CHECK': '1',
                       'TERM': 'xterm-256color', 'COLORTERM': 'truecolor', 'LANG': 'en_US.UTF-8',
                       'GOMAXPROCS': '2', 'COLORFGBG': '0;15' if args.color == 'light' else '15;0'}
        if args.color == 'mono':
            environment['NO_COLOR'] = '1'
        class Handler(fixture.handler()):
            def route(self, state, route, query):
                result = super().route(state, route, query)
                if args.has_more and route == '/batches':
                    result['has_more'] = True
                return result
        with HTTPServer(('127.0.0.1', 0), Handler) as server:
            worker = threading.Thread(target=server.serve_forever)
            worker.start()
            environment['OPENAI_BASE_URL'] = f'http://127.0.0.1:{server.server_port}/v1'
            terminal = None
            try:
                terminal = harness.Terminal(str(args.binary), ['batches', 'browse'], environment,
                                            width=args.width, height=args.height,
                                            cwd=home if args.save_path else None,
                                            max_output_bytes=1_000_000, deadline=time.monotonic()+45)
                terminal.wait('Search  ')
                settle(terminal)
                def save(name):
                    header = {'version': 2, 'width': args.width, 'height': args.height,
                              'title': 'Batch browser; actual CLI, synthetic data', 'env': {'TERM': 'xterm-256color'}}
                    (args.output/(name+'.cast')).write_text('\n'.join(json.dumps(row) for row in [header,*terminal.events])+'\n')
                    screen = harness.screen(terminal.raw, width=args.width, height=args.height).decode()
                    (args.output/(name+'.txt')).write_text(screen+'\n')
                    result['screens'][name] = {'events': len(terminal.events), 'text': screen}
                    return screen
                if args.query:
                    terminal.send(args.query.encode())
                    terminal.send(b'\x1b[D')
                    settle(terminal)
                if args.search_focus:
                    terminal.send(b'\x1b[A')
                    settle(terminal)
                if args.select_rows:
                    terminal.send(b'\x1b[B')
                    settle(terminal)
                save('list')
                if args.actions or args.message or args.expand_save or args.save_path:
                    processing = args.message in ('pending','input','details') and not (args.partial or args.success)
                    batch_id = 'batch_processing_demo' if processing else ('batch_demo' if args.success else 'batch_partial_demo')
                    if processing:
                        terminal.send(b'\x1b[B'*2)
                    elif not args.success:
                        terminal.send(b'\x1b[B')
                    settle(terminal)
                    save('partial')
                    terminal.send(b'\r')
                    terminal.wait('View results')
                    terminal.wait('1 of 3 succeeded' if processing else ('3 of 3 succeeded' if args.success else '2 of 3 succeeded'))
                    settle(terminal)
                    actions_screen = save('actions')
                    if args.expand_save or args.save_path:
                        index = action_index(actions_screen, 'Save a file')
                        terminal.send(b'\x1b[B'*index+b'\r')
                        settle(terminal)
                        expanded_screen = save('expanded-save')
                        assert batch_id in expanded_screen, 'expanded save context missing'
                        assert 'Save a file' in expanded_screen and '▾' in expanded_screen, 'save action did not expand'
                        assert 'Output' in expanded_screen and 'Input' in expanded_screen, 'save file children missing'
                        if not args.success:
                            assert 'Error' in expanded_screen, 'error file child missing'
                        if args.save_path:
                            selected = [line.split('>', 1)[1].strip(' │')
                                        for line in expanded_screen.splitlines() if line.lstrip(' │').startswith('>')]
                            assert selected == ['Output file'], 'expansion did not select the first file child'
                            terminal.send(b'\r')
                            settle(terminal)
                            destination = './output.jsonl'
                            terminal.send(b'\x01\x0b'+destination.encode())
                            settle(terminal)
                            path_screen = save('save-path')
                            assert batch_id in path_screen, 'inline destination lost batch identity'
                            for label in ('View results', 'View input', 'Batch details', 'Save a file', 'Output file'):
                                assert label in path_screen, ('inline destination lost action context', label)
                            assert destination in path_screen, 'inline destination did not retain typed path'
                            terminal.send(b'\x1b')
                            settle(terminal)
                            returned_screen = save('returned-save')
                            assert returned_screen == expanded_screen, 'destination cancellation changed the selected file child'
                            result['destination'] = destination
                            result['destination_not_saved'] = not (Path(home)/destination).exists()
                            assert result['destination_not_saved'], 'capture unexpectedly saved a destination file'
                        terminal.send(b'\x1b')
                        settle(terminal)
                        collapsed_screen = save('collapsed-save')
                        assert batch_id in collapsed_screen, 'collapse left the batch context'
                        assert 'Save a file' in collapsed_screen and '▸' in collapsed_screen, 'Escape did not collapse save action'
                        assert '▾' not in collapsed_screen, 'save action remained expanded'
                    if args.message:
                        label = {'pending':'View results','results':'View results','errors':'View errors','input':'View input','details':'Batch details'}[args.message]
                        index = action_index(actions_screen, label)
                        at = len(terminal.raw)
                        terminal.send(b'\x1b[B'*index+b'\r')
                        records = args.message in ('input', 'results', 'errors')
                        if records:
                            terminal.wait('Ctrl+V', after=at)
                            settle(terminal)
                            request_screen = save('requests')
                            if args.message == 'errors':
                                preview = save('inline')
                                if args.height >= 16 and args.width >= 50:
                                    assert '500' in preview, 'sole error did not expand automatically'
                            elif args.width >= 50 and args.height >= 16:
                                terminal.send(b'\r')
                                settle(terminal)
                                preview = save('inline')
                                assert 'Search' in preview and preview != request_screen, 'request did not expand in place'
                                terminal.send(b'\x1b')
                                settle(terminal)
                                assert save('collapsed') == request_screen, 'collapsing changed the request list'
                            at = len(terminal.raw)
                            terminal.send(b'\x1b[C')
                        terminal.wait('PgUp/PgDn', after=at)
                        settle(terminal)
                        message_screen = save('message')
                        if args.message == 'details' and args.width >= 50 and args.height >= 12:
                            assert batch_id in message_screen, 'batch details context missing'
                        if records:
                            request_id = {'input':'sentiment-positive', 'results':'sentiment-neutral', 'errors':'sentiment-negative'}[args.message]
                            if args.width >= 50 and args.height >= 12:
                                assert request_id in request_screen, 'selected request missing from list'
                                assert batch_id + ' / Record 1' in message_screen, 'selected record context missing'
                                assert request_id in message_screen, 'selected request identity missing from details'
                            at = len(terminal.raw)
                            terminal.send(b'j')
                            terminal.wait('Raw JSON', after=at)
                            settle(terminal)
                            raw_screen = save('raw')
                            if args.width >= 50 and args.height >= 12:
                                assert batch_id + ' / Record 1' in raw_screen, 'raw record context missing'
                                assert request_id in raw_screen, 'raw request identity missing'
                            at = len(terminal.raw)
                            terminal.send(b'\x1b')
                            terminal.wait('Ctrl+V', after=at)
                            settle(terminal)
                            returned_screen = save('returned-requests')
                            assert returned_screen == request_screen, 'request list or selection changed after details'
                        at = len(terminal.raw)
                        terminal.send(b'\x1b')
                        terminal.wait('View results', after=at)
                        settle(terminal)
                    at = len(terminal.raw)
                    terminal.send(b'\x1b')
                    terminal.wait('Search  ', after=at)
                    settle(terminal)
                terminal.send(b'\x1b')
                terminal.finish(0, expect_alternate_screen=True)
                result['exit'] = terminal.child.returncode
                result['terminal_restored'] = True
            finally:
                try:
                    if terminal:
                        terminal.close()
                finally:
                    server.shutdown()
                    worker.join(5)
            result['server_stopped'] = not worker.is_alive()
    result['fixture_failures'] = fixture.failures
    log.close()
    result['requests'] = [json.loads(line) for line in (args.output/'requests.jsonl').read_text().splitlines()]
    assert not fixture.failures and result['server_stopped']
    assert all(row['method'] == 'GET' for row in result['requests'])
    if args.save_path:
        assert all(not row['path'].endswith('/content') for row in result['requests']), 'destination capture fetched file content'
    (args.output/'evidence.json').write_text(json.dumps(result, indent=2)+'\n')
    print(args.output)


if __name__ == '__main__':
    main()
