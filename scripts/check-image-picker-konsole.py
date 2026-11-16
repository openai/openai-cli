#!/usr/bin/env python3
"""Check blocked Konsole picker diagnostics through the public CLI.

    python3 -I -B scripts/check-image-picker-konsole.py BINARY OUTPUT

Uses synthetic loopback responses and private, non-controlling POSIX PTYs.
stdout and stderr share each PTY. These checks do not prove native rendering.
Requires permission to inspect owned processes with ps. Retains failure receipts.
"""
import argparse
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import platform
import signal
import sys
import tempfile
import termios
import threading
import time
import traceback

SCRIPTS = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
from image_picker_harness import ANSI, Terminal, capture_fixture

spec = importlib.util.spec_from_file_location('konsole_checks', SCRIPTS / 'check-image-konsole.py')
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)
require = checks.require
NATIVE, ENTER = b'\x1b]1337;File=', b'\x1b[?1049h'
PROMPT = 'SYNTHETIC Konsole blocked diagnostics'
CASES = ('shared-stderr', 'shared-stderr-draft-failure')
BLOCKED_SECONDS = 9


def digest(path):
    value = hashlib.sha256()
    with Path(path).open('rb') as source:
        for chunk in iter(lambda: source.read(1 << 20), b''):
            value.update(chunk)
    return value.hexdigest()


def process_table():
    # Never collect unrelated process arguments or environments.
    result = capture_fixture(
        ['/bin/ps', '-axo', 'pid=,ppid=,pgid=,lstart=,comm='],
        env={'PATH': '/usr/bin:/bin', 'LC_ALL': 'C'}, timeout=3, limit=4 << 20)
    require(result.returncode == 0, 'ps could not inspect owned processes')
    table = {}
    for line in result.stdout.decode('utf-8', 'replace').splitlines():
        fields = line.split(None, 8)
        if len(fields) == 9:
            pid, parent, group = map(int, fields[:3])
            table[pid] = dict(pid=pid, parent=parent, group=group,
                              started=' '.join(fields[3:8]), executable=fields[8])
    return table


def same_process(before, after):
    return after is not None and all(before[key] == after[key]
                                     for key in ('pid', 'started', 'executable'))


def observe_helpers(root, owned):
    table = process_table()
    parents = {item['pid'] for item in [root, *owned.values()]
               if same_process(item, table.get(item['pid']))}
    while True:
        children = [item for pid, item in table.items()
                    if item['parent'] in parents and pid not in parents]
        if not children:
            break
        for item in children:
            owned[item['pid']] = item
            parents.add(item['pid'])
    return [item for item in owned.values() if same_process(item, table.get(item['pid']))]


class Fixture(checks.fixtures.Fixture):
    def setup(self):
        super().setup()
        self.connection.settimeout(3)

    def do_POST(self):
        length = int(self.headers.get('Content-Length', '0'))
        if not 0 < length < 1 << 20:
            self.server.case.errors.append('synthetic request exceeded fixture bound')
            self.send_error(400)
            return
        super().do_POST()


def until(terminal, predicate, label, timeout=12):
    deadline = min(terminal.deadline, time.monotonic() + timeout)
    while not predicate():
        require(terminal.child.poll() is None, label + ': CLI exited early')
        require(time.monotonic() < deadline, label + ': deadline expired')
        terminal.read(.02)


def pump(terminal, duration):
    deadline = min(terminal.deadline, time.monotonic() + duration)
    while time.monotonic() < deadline and terminal.child.poll() is None:
        terminal.read(min(.02, deadline - time.monotonic()))


def drain_for_cleanup(terminal):
    deadline = min(terminal.deadline, time.monotonic() + 2)
    quiet = time.monotonic() + .1
    while time.monotonic() < deadline:
        previous = len(terminal.raw)
        terminal.read(.02)
        if len(terminal.raw) != previous:
            quiet = time.monotonic() + .1
        if terminal.child.poll() is not None and time.monotonic() >= quiet:
            break


def saved_files(home):
    return sorted((home / 'Downloads' / 'gpt-images').glob('*.png'))


def saved_hashes(home):
    return {str(path.relative_to(home)): digest(path) for path in saved_files(home)}


def run_case(binary, output, name, png, results):
    directory = output / name
    directory.mkdir()
    row = dict(case=name, result='fail', expected_exit=1, bound_seconds=BLOCKED_SECONDS,
               cleanup_errors=[], forced_cleanup=[], helpers=[], processes_ended=False)
    results.append(row)
    temporary = tempfile.TemporaryDirectory(prefix='konsole-picker-')
    home = Path(temporary.name)
    terminal, server, thread, root = None, None, None, None
    owned = {}
    started = time.monotonic()
    deadline = started + 40
    response = checks.Response('ok', png)

    def cleanup(action):
        try:
            action()
        except BaseException as error:
            row['cleanup_errors'].append(repr(error))

    try:
        server = http.server.HTTPServer(('127.0.0.1', 0), Fixture)
        server.case = response
        thread = threading.Thread(target=lambda: server.serve_forever(poll_interval=.05), daemon=True)
        thread.start()
        env = dict(PATH='/usr/bin:/bin', HOME=str(home), XDG_CONFIG_HOME=str(home / 'config'),
                   LANG='C.UTF-8', TERM='xterm-256color', TERM_PROGRAM='', KONSOLE_VERSION='250402',
                   COLORTERM='truecolor', SHELL='/bin/bash', OPENAI_PICKER_SHELL='bash',
                   OPENAI_API_KEY='synthetic-konsole-check-key',
                   OPENAI_BASE_URL=f'http://127.0.0.1:{server.server_port}/v1', GOMAXPROCS='2',
                   OPENAI_CLI_NO_UPDATE_CHECK='1', OPENAI_CLI_NO_AUTO_UPDATE='1')
        terminal = Terminal(str(binary), ['images', 'generate'], env, width=96, height=28,
                            controlling_terminal=False, stderr_file=None, deadline=deadline,
                            max_output_bytes=8 << 20, max_events=10000, max_event_bytes=12 << 20)
        row['root_pid'] = terminal.child.pid
        root = process_table().get(terminal.child.pid)
        require(root is not None, 'owned CLI identity was unavailable')
        until(terminal, lambda: b'Create image' in terminal.raw, 'initial picker')
        pump(terminal, .25)
        observe_helpers(root, owned)
        require(owned, 'no prepared output helpers were observed')
        terminal.send(PROMPT.encode())
        pump(terminal, .1)
        mark = len(terminal.raw)
        terminal.send(b'\r')

        def saved():
            text = ANSI.sub('', terminal.raw[mark:].decode('utf-8', 'replace'))
            position = text.rfind('Saved in ')
            return position >= 0 and 'Ctrl+C exit' in text[position:] and len(saved_files(home)) == 1

        until(terminal, saved, 'completed image save')
        row['fixture_sha256'] = hashlib.sha256(png).hexdigest()
        row['saved_before'] = saved_hashes(home)
        require(list(row['saved_before'].values()) == [row['fixture_sha256']], 'saved original differs from fixture')
        require(NATIVE not in terminal.raw, 'native output entered the ordinary Konsole panel')
        (directory / 'saved-original.png').write_bytes(saved_files(home)[0].read_bytes())

        if name.endswith('draft-failure'):
            # Respect the existing 750 ms post-request input guard.
            pump(terminal, .9)
            terminal.send(b'\x1b[200~ CHANGED-DRAFT\x1b[201~')
            until(terminal, lambda: 'CHANGED-DRAFT' in terminal.text(), 'changed draft')
            paths = list(home.rglob('image-picker.json'))
            require(len(paths) == 1 and paths[0].is_file(), 'saved draft was unavailable')
            draft = paths[0]
            row['draft_before_sha256'] = digest(draft)
            (directory / 'original-draft.json').write_bytes(draft.read_bytes())
            draft.unlink()
            draft.mkdir()
            row['draft_failure'] = 'Replaced only the private fixture draft with a directory.'

        terminal.send(b'\x1b')
        pump(terminal, .15)
        terminal.send(b'\x1b[Z')
        pump(terminal, .15)
        mark = len(terminal.raw)
        terminal.send(b'\r')
        until(terminal, lambda: NATIVE in terminal.raw[mark:], 'Expand native transfer')
        frame = terminal.raw.find(NATIVE, mark)
        require(ENTER in terminal.raw[mark:frame], 'native transfer did not enter Expand')
        require(b'\x07' not in terminal.raw[frame:] and b'\x1b\\' not in terminal.raw[frame:],
                'fixture missed the incomplete native transfer')
        row['transfer_incomplete_when_reader_stopped'] = True
        require(time.monotonic() + BLOCKED_SECONDS < deadline, 'insufficient blocked-output observation time')
        stopped = time.monotonic()
        next_snapshot = stopped
        # Do not read, close, or send input through the PTY during this interval.
        while time.monotonic() - stopped < BLOCKED_SECONDS and terminal.child.poll() is None:
            if time.monotonic() >= next_snapshot:
                observe_helpers(root, owned)
                next_snapshot = time.monotonic() + .25
            time.sleep(.025)
        row['undrained_seconds'] = round(time.monotonic() - stopped, 3)
        row['exit_while_undrained'] = terminal.child.returncode
        row['helpers_at_exit_or_bound'] = observe_helpers(root, owned)
        row['saved_at_exit_or_bound'] = saved_hashes(home)
        require(row['saved_at_exit_or_bound'] == row['saved_before'], 'saved original changed during blocked output')
        require(terminal.child.returncode == 1, 'shared stdout/stderr prevented bounded exit 1')
        require(not row['helpers_at_exit_or_bound'], 'owned helper survived CLI exit')
        require(len(response.requests) == 1 and response.requests[0]['prompt'] == PROMPT,
                'generation retried or submitted the changed draft')
        require(not response.errors, 'synthetic API fixture failed: ' + repr(response.errors))
        restored, initial = termios.tcgetattr(terminal.slave), list(terminal.initial)
        if sys.platform == 'darwin':
            restored[3] &= ~termios.PENDIN
            initial[3] &= ~termios.PENDIN
        require(restored == initial, 'terminal input/output modes were not restored')
        row['termios_restored'] = True
        if name.endswith('draft-failure'):
            require(draft.is_dir(), 'draft failure fixture changed unexpectedly')
            require(digest(directory / 'original-draft.json') == row['draft_before_sha256'], 'original draft copy changed')
        row['result'] = 'pass'
    except BaseException as error:
        row.update(error=repr(error), traceback=traceback.format_exc(),
                   interrupted=isinstance(error, (KeyboardInterrupt, InterruptedError)))
    finally:
        if terminal is not None:
            if root is not None:
                cleanup(lambda: observe_helpers(root, owned))
            # A failed bound stays failed. Resume reading only for evidence and cleanup.
            cleanup_started = time.monotonic()
            cleanup(lambda: drain_for_cleanup(terminal))
            row['exit_after_cleanup_drain'] = terminal.child.poll()
            row['cleanup_drain_seconds'] = round(time.monotonic() - cleanup_started, 3)
            if terminal.child.returncode is None:
                row['forced_cleanup'].append(terminal.child.pid)
            cleanup(terminal.close)
            row['actual_exit'] = terminal.child.returncode
            cleanup(lambda: (directory / 'combined-output.raw').write_bytes(terminal.raw))
        if root is not None:
            try:
                for item in observe_helpers(root, owned):
                    # Recheck the recorded identity immediately before signaling.
                    if same_process(item, process_table().get(item['pid'])):
                        try:
                            os.kill(item['pid'], signal.SIGKILL)
                            row['forced_cleanup'].append(item['pid'])
                        except ProcessLookupError:
                            pass
                cleanup_deadline = time.monotonic() + 2
                while True:
                    row['remaining_owned_helpers'] = observe_helpers(root, owned)
                    if not row['remaining_owned_helpers'] or time.monotonic() >= cleanup_deadline:
                        break
                    time.sleep(.05)
            except BaseException as error:
                row['cleanup_errors'].append(repr(error))
        if server is not None:
            if thread is not None and thread.is_alive():
                cleanup(server.shutdown)
            cleanup(server.server_close)
        if thread is not None:
            cleanup(lambda: thread.join(timeout=2))
            if thread.is_alive():
                row['cleanup_errors'].append('synthetic HTTP thread did not stop')
        cleanup(lambda: row.update(saved_after=saved_hashes(home)))
        if 'saved_before' in row and row['saved_before'] != row.get('saved_after'):
            row['result'] = 'fail'
            row.setdefault('error', 'saved originals changed before cleanup')
        row['helpers'] = list(owned.values())
        row['requests'] = response.requests
        row['fixture_errors'] = response.errors
        cleanup(temporary.cleanup)
        row['processes_ended'] = (not row['cleanup_errors'] and not row.get('remaining_owned_helpers')
                                  and (terminal is None or (terminal.child.returncode is not None
                                      and root is not None and 'remaining_owned_helpers' in row)))
        if row['forced_cleanup'] or not row['processes_ended']:
            row['result'] = 'fail'
        row['elapsed_seconds'] = round(time.monotonic() - started, 3)
        (directory / 'results.json').write_text(json.dumps(row, indent=2) + '\n')
    return row


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    binary, output = args.binary.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    require(not any((output / name).exists() for name in (*CASES, 'results.json')),
            'use a fresh evidence directory; existing receipts remain immutable')
    evidence = dict(binary=str(binary), checker_sha256=digest(__file__),
                    host=dict(system=platform.system(), machine=platform.machine(), python=platform.python_version()),
                    scope='Public CLI, synthetic Konsole markers, shared non-controlling PTY; no native rendering proof.',
                    results=[], processes_ended=False)

    def interrupted(number, _frame):
        raise InterruptedError(f'checker received signal {number}')

    previous = {number: signal.signal(number, interrupted) for number in (signal.SIGTERM, signal.SIGHUP)}
    try:
        evidence['binary_sha256'] = digest(binary)
        evidence['helpers_sha256'] = {name: digest(SCRIPTS / name) for name in
                                      ('image_picker_harness.py', 'check-image-konsole.py',
                                       'check-image-vscode.py', 'terminal_test.py')}
        png = checks.large_png()
        for name in CASES:
            row = run_case(binary, output, name, png, evidence['results'])
            print(row['result'].upper(), name, flush=True)
            if row.get('interrupted') or not row['processes_ended']:
                break
        evidence['binary_sha256_after'] = digest(binary)
        require(evidence['binary_sha256_after'] == evidence['binary_sha256'], 'binary changed during the checks')
    except BaseException as error:
        evidence.update(error=repr(error), traceback=traceback.format_exc())
    finally:
        for number, handler in previous.items():
            signal.signal(number, handler)
        evidence['processes_ended'] = all(row['processes_ended'] for row in evidence['results'])
        evidence['result'] = ('pass' if len(evidence['results']) == len(CASES) and 'error' not in evidence
                              and all(row['result'] == 'pass' for row in evidence['results']) else 'fail')
        (output / 'results.json').write_text(json.dumps(evidence, indent=2) + '\n')
    return 0 if evidence['result'] == 'pass' else 1


if __name__ == '__main__':
    raise SystemExit(main())
