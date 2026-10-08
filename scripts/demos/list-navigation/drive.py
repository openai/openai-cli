#!/usr/bin/env python3
"""Drive one synthetic list scene inside the shared asciinema capture."""
import argparse
import importlib.util
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import termios
import time
import urllib.parse
import urllib.request


spec = importlib.util.spec_from_file_location(
    'list_demo_terminal', pathlib.Path(__file__).resolve().parents[2]/'image_picker_harness.py')
terminal_module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(terminal_module)


class DemoTerminal(terminal_module.Terminal):
    def read(self, duration=0.05):
        mark = len(self.raw)
        super().read(duration)
        sys.stdout.buffer.write(self.raw[mark:])
        sys.stdout.buffer.flush()


def drain(terminal, seconds):
    deadline = time.monotonic()+seconds
    while time.monotonic() < deadline:
        terminal.read(min(0.03, max(0, deadline-time.monotonic())))


def screen_text(terminal, asciinema):
    # Incremental terminal redraws can replace only one digit of a file ID.
    # Reconstruct the screen with the recorder's existing terminal emulator.
    cache_key = (len(terminal.raw), len(terminal.events))
    if getattr(terminal, '_screen_key', None) == cache_key:
        return terminal._screen_text
    with tempfile.TemporaryDirectory(prefix='list-demo-screen-') as temporary:
        recording = pathlib.Path(temporary)/'screen.cast'
        width, height = terminal.initial_size
        header = {'version': 2, 'width': width, 'height': height}
        recording.write_text('\n'.join(json.dumps(value) for value in [header, *terminal.events])+'\n')
        result = subprocess.run([asciinema, 'convert', '-f', 'txt', str(recording), '-'],
                                capture_output=True, text=True, check=True, timeout=5,
                                env={'PATH': '/usr/bin:/bin', 'HOME': temporary,
                                     'ASCIINEMA_STATE_HOME': temporary+'/state',
                                     'ASCIINEMA_CONFIG_HOME': temporary+'/config'})
    terminal._screen_key, terminal._screen_text = cache_key, result.stdout
    return result.stdout


def wait_screen(terminal, asciinema, *markers):
    deadline = time.monotonic()+12
    while True:
        terminal.read()
        text = screen_text(terminal, asciinema)
        if all(marker in text for marker in markers):
            return text
        if terminal.child.poll() is not None:
            # A fast baseline can exit while the PTY still has unread output.
            drain(terminal, 0.1)
            text = screen_text(terminal, asciinema)
            assert all(marker in text for marker in markers), ('command exited before rendering', markers, text[-1500:])
            return text
        assert time.monotonic() < deadline, ('screen markers missing', markers, text[-1500:])


def count_requests(url):
    parsed = urllib.parse.urlsplit(url)
    assert parsed.scheme == 'http' and parsed.hostname == '127.0.0.1', 'fixture must use loopback HTTP'
    with urllib.request.urlopen(url, timeout=2) as response:
        assert response.status == 200
        return json.load(response)['requests']


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--scene', choices=('before', 'after'), required=True)
    parser.add_argument('--counts-url', required=True)
    parser.add_argument('--evidence', required=True)
    parser.add_argument('--asciinema', default=os.environ.get('DEMO_ASCIINEMA', 'asciinema'))
    args = parser.parse_args()
    assert all(os.isatty(fd) for fd in (0, 1, 2)), 'capture must provide terminal input and output'
    binary = shutil.which('openai')
    assert binary, 'capture did not provide its selected binary'
    asciinema = shutil.which(args.asciinema)
    assert asciinema, 'capture did not provide its asciinema executable'
    started = time.monotonic()
    evidence = {'scene': args.scene, 'command': 'openai files list --limit 2 --max-items -1',
                'keys': [], 'milestones': [], 'screens': {}}

    def mark(name):
        value = {'name': name, 'requests': count_requests(args.counts_url),
                 'elapsed_seconds': round(time.monotonic()-started, 3)}
        evidence['milestones'].append(value)
        return value['requests']

    print('$ openai files list --limit 2 --max-items -1', flush=True)
    with tempfile.TemporaryDirectory(prefix='list-demo-home-') as home:
        env = {'PATH': '/usr/bin:/bin', 'HOME': home, 'TERM': 'xterm-256color', 'LANG': 'en_US.UTF-8',
               'NO_COLOR': '1', 'FORCE_COLOR': '0', 'OPENAI_API_KEY': 'synthetic-demo-key',
               'OPENAI_BASE_URL': os.environ['OPENAI_BASE_URL']}
        evidence['settings'] = {name: env[name] for name in ('TERM', 'LANG', 'NO_COLOR', 'FORCE_COLOR')}
        evidence['settings'].update({'width': 90, 'height': 20, 'page_size': 2, 'max_items': -1})
        terminal = DemoTerminal(binary, ['files', 'list', '--limit', '2', '--max-items', '-1'],
                                env, width=90, height=20)
        try:
            markers = ('file_001', 'file_002')
            if args.scene == 'after':
                markers += ('Space: more   q: quit',)
            evidence['screens']['first-page'] = wait_screen(terminal, asciinema, *markers)
            mark('first-visible')
            drain(terminal, 1.5)
            idle = mark('idle-before-input')
            assert not evidence['keys']
            assert idle == (2 if args.scene == 'before' else 1), ('idle requests', idle)
            if args.scene == 'after':
                assert terminal.child.poll() is None, 'command exited without waiting'
                evidence['keys'].append({'key': 'Space', 'elapsed_seconds': round(time.monotonic()-started, 3)})
                terminal.send(b' ')
                evidence['screens']['last-page'] = wait_screen(
                    terminal, asciinema, 'file_003', 'file_004', 'End of results')
                assert mark('after-space') == 2
                drain(terminal, 1.5)
                assert mark('last-page-idle') == 2
                evidence['keys'].append({'key': 'q', 'elapsed_seconds': round(time.monotonic()-started, 3)})
                terminal.send(b'q')
            deadline = time.monotonic()+5
            while terminal.child.poll() is None:
                terminal.read()
                assert time.monotonic() < deadline, 'command did not exit'
            drain(terminal, 0.1)
            evidence['exit_status'] = terminal.child.returncode
            assert terminal.child.returncode == 0, terminal.text()[-1000:]
            assert mark('finished') == 2
            evidence['screens']['finished'] = screen_text(terminal, asciinema)
            restored, initial = termios.tcgetattr(terminal.slave), terminal.initial[:]
            if sys.platform == 'darwin':
                restored[3] &= ~termios.PENDIN
                initial[3] &= ~termios.PENDIN
            assert restored == initial, 'terminal input mode was not restored'
            assert not re.search(rb'\x1b\[\?(?:47|1047|1049)[hl]', terminal.raw), 'unexpected alternate screen'
        finally:
            terminal.close()
    with open(args.evidence, 'x') as stream:
        json.dump(evidence, stream, indent=2)
        stream.write('\n')
        stream.flush()
    print('\nObserved API requests before input: '+str(idle), flush=True)
    if args.scene == 'after':
        print('Space requested page 2. q returned to the shell.', flush=True)
    else:
        print('Both pages arrived without keyboard input.', flush=True)
    print('$ ', end='', flush=True)
    time.sleep(2)


if __name__ == '__main__':
    main()
