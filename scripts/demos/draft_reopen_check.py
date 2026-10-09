"""Bounded draft reopen checks with synthetic API and native PTY helpers.

Use --case unsent --before for main before draft persistence.
The auth-reopen --before comparison requires compact-auth baseline 39e26577.
"""
import argparse
import fcntl
import hashlib
import json
import os
import pathlib
import re
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

REPO = pathlib.Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO / 'scripts/demos'))
from image_recovery_check import Fixture, Process, PROMPT, environment, preference, saved

ANSI = re.compile(rb'\x1b(?:\[[0-?]*[ -/]*[@-~]|[78])')
EDITED = 'A synthetic orange robot with blue shoes'
KEY = 'synthetic-draft-reopen-key'
CASES = ('unsent', 'clear', 'queued-enter', 'newer-state', 'corrupt', 'v99',
         'sigterm', 'sighup', 'flags', 'json')


def fixture():
    api = Fixture(fail=True, delay=0.2)
    api.error_status = 401
    api.events = []
    original = api.server.RequestHandlerClass

    class TrackedHandler(original):
        def do_POST(self):
            api.events.append(dict(method='POST', path=self.path))
            super().do_POST()

        def do_GET(self):
            api.events.append(dict(method='GET', path=self.path))
            self.send_response(400)
            self.send_header('Content-Length', '0')
            self.end_headers()

    api.server.RequestHandlerClass = TrackedHandler
    return api


def state_file(home):
    return home / 'Library/Application Support/openai/image-picker.json'


def seed(home, prompt='', version=2):
    path = preference(home, '2')
    record = json.loads(path.read_text())
    record.update(version=version)
    if version != 1:
        record['prompt'] = prompt
    path.write_text(json.dumps(record, indent=2) + '\n')
    path.chmod(0o600)
    return record


def replace_state(path, record):
    temporary = path.with_suffix('.replacement')
    temporary.write_text(json.dumps(record, indent=2) + '\n')
    temporary.chmod(0o600)
    temporary.replace(path)


def current_frame(process):
    raw = bytes(process.output)
    return ANSI.sub(b'', raw[raw.rfind(b'Create image'):])


def assert_frame(process, prompt, quality='high'):
    frame = current_frame(process)
    for text in (b'gpt-image-2', b'1536 x 1024', b'Opaque', b'More options'):
        assert text in frame, ('missing setting', text)
    assert re.search(rb'Quality\s+' + quality.title().encode(), frame), frame
    assert re.search(rb'Images\s+2', frame)
    if prompt:
        assert prompt.encode() in frame, ('missing restored prompt', prompt)
    return frame


def edit_prompt(process, prompt):
    process.send(b'\x05\x15')  # End, then clear the prompt before the cursor.
    process.send(prompt)
    process.pump(0.2)


def low_quality(process):
    process.send(b'\t\x1b[B\x1b[B\r')
    process.pump(0.2)
    process.send(b'\x1b[A\x1b[A\r')
    process.pump(0.2)
    process.send(b'\x1b')
    process.pump(0.2)
    assert re.search(rb'Quality\s+Low', current_frame(process)), 'quality edit did not commit'


def check(binary, output, case, before=False, mirror=False):
    api = fixture()
    processes = []
    result = dict(case=case, before=before)
    output.mkdir(parents=True, exist_ok=True)
    try:
        with tempfile.TemporaryDirectory(prefix='draft-reopen-') as directory:
            home = pathlib.Path(directory)
            env = environment(home, api)
            env.update(OPENAI_API_KEY=KEY, GOMAXPROCS='2', TERM_SESSION_ID='')
            env['PATH'] = str(binary.parent) + ':' + env['PATH']
            if mirror:
                env.pop('NO_COLOR', None)
            path = state_file(home)

            def start(label):
                process = Process(binary, env, mirror=mirror)
                processes.append((label, process))
                fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
                process.ready()
                return process

            def snapshot(label, process):
                (output / (case + '.' + label + '.pty.bin')).write_bytes(process.output)

            def stop(process, code=130):
                assert process.stop() == code

            def stored(prompt, quality='high'):
                record = json.loads(path.read_text())
                assert record == dict(version=2, prompt=prompt, model='gpt-image-2', size='1536x1024',
                                      quality=quality, count='2', background='opaque', format='png', output_dir=''), record
                assert path.stat().st_mode & 0o777 == 0o600
                return record

            if case == 'auth-reopen':
                seed(home, version=1)
                process = start('initial')
                process.submit()
                process.wait_for(lambda: len(api.requests) == 1 and b'Authentication failed' in process.output)
                process.wait_for(lambda: bytes(process.output).count(b'Create image') >= 2)
                process.pump(1.0)
                if not before:
                    assert b'Draft saved.' in process.output
                    assert b'Copy your prompt.' not in process.output
                    stored(PROMPT)
                snapshot('failure', process)
                edit_prompt(process, EDITED)
                low_quality(process)
                assert_frame(process, EDITED, 'low')
                snapshot('edited', process)
                process.pump(0.6)
                stop(process)
                if not before:
                    stored(EDITED, 'low')
                if mirror:
                    print('\n$ openai images generate  # reopen', flush=True)
                reopened = start('reopened')
                reopened.pump(1.2)
                if before:
                    assert PROMPT.encode() not in current_frame(reopened)
                    assert EDITED.encode() not in current_frame(reopened)
                    assert_frame(reopened, '', 'high')
                else:
                    assert_frame(reopened, EDITED, 'low')
                    stored(EDITED, 'low')
                snapshot('reopened', reopened)
                stop(reopened)
                result.update(restored_after_exit=not before, edited_quality='low', idle_seconds=1.2)
            elif case == 'unsent':
                seed(home, version=1)
                process = start('initial')
                edit_prompt(process, EDITED)
                low_quality(process)
                snapshot('edited', process)
                stop(process)
                if before:
                    assert json.loads(path.read_text()) == dict(version=1, model='gpt-image-2', size='1536x1024',
                                                               quality='high', count='2', background='opaque',
                                                               format='png', output_dir='')
                else:
                    stored(EDITED, 'low')
                previous = path.read_bytes()
                if mirror:
                    print('\n$ openai images generate  # reopen', flush=True)
                reopened = start('reopened')
                reopened.pump(1.2)
                if before:
                    assert EDITED.encode() not in current_frame(reopened)
                    assert_frame(reopened, '', 'high')
                else:
                    assert_frame(reopened, EDITED, 'low')
                snapshot('reopened', reopened)
                stop(reopened)
                assert path.read_bytes() == previous, 'untouched reopen rewrote the draft'
                result.update(restored_after_exit=not before, edited_quality='low', idle_seconds=1.2)
            elif case == 'clear':
                seed(home, EDITED)
                process = start('initial')
                assert_frame(process, EDITED)
                edit_prompt(process, '')
                stop(process)
                stored('')
                reopened = start('reopened')
                assert EDITED.encode() not in current_frame(reopened)
                snapshot('reopened', reopened)
                stop(reopened)
                stored('')
            elif case == 'queued-enter':
                seed(home, EDITED)
                process = start('initial')
                process.send(b'\r\r\r\r')
                process.pump(1.0)
                assert not api.events, 'queued Enter submitted the restored draft'
                assert process.status is None
                snapshot('queued', process)
                process.send(b'\r')
                process.wait_for(lambda: len(api.requests) == 1 and b'Draft saved.' in process.output)
                process.pump(0.9)
                stop(process)
                assert api.requests[0]['prompt'] == EDITED
                result.update(queued_requests=0, deliberate_requests=1)
            elif case == 'newer-state':
                seed(home, PROMPT)
                process = start('initial')
                assert_frame(process, PROMPT)
                replacement = json.loads(path.read_text())
                replacement.update(prompt=EDITED, quality='low')
                replace_state(path, replacement)
                previous = path.read_bytes()
                stop(process)
                assert path.read_bytes() == previous, 'untouched session overwrote a newer draft'
                stored(EDITED, 'low')
            elif case in ('corrupt', 'v99'):
                seed(home, 'Private synthetic stored draft', version=99)
                if case == 'corrupt':
                    path.write_bytes(b'{synthetic corrupt state')
                previous = path.read_bytes()
                process = start('initial')
                assert b'Could not restore the draft. Existing data is kept.' in process.output
                edit_prompt(process, EDITED)
                stop(process)
                assert b'Could not save the draft.' in process.output
                assert b'Private synthetic stored draft' not in process.output
                assert path.read_bytes() == previous
            elif case in ('sigterm', 'sighup'):
                seed(home, version=1)
                process = start('initial')
                edit_prompt(process, EDITED)
                received, code = (signal.SIGTERM, 143) if case == 'sigterm' else (signal.SIGHUP, 129)
                os.kill(process.pid, received)
                process.wait_for(lambda: process.status is not None)
                assert process.status == code, (case, process.status)
                stored(EDITED)
                result['exit_status'] = code
                reopened = start('reopened')
                assert_frame(reopened, EDITED)
                snapshot('reopened', reopened)
                stop(reopened)
            elif case in ('flags', 'json'):
                seed(home, 'Private synthetic stored draft', version=99)
                previous = path.read_bytes()
                args = ['images', 'generate', '--prompt', PROMPT, '--inline', 'off']
                if case == 'json':
                    args = ['--format', 'json', '--format-error', 'json'] + args
                completed = subprocess.run([str(binary), *args], env=env, input=b'', capture_output=True, timeout=12)
                for stream in ('stdout', 'stderr'):
                    (output / (case + '.' + stream)).write_bytes(getattr(completed, stream))
                assert completed.returncode == 1
                if case == 'json':
                    json.loads(completed.stderr)
                assert b'draft' not in (completed.stdout + completed.stderr).lower()
                assert path.read_bytes() == previous
                assert api.requests[0]['prompt'] == PROMPT
                if case == 'json':
                    assert api.requests[0] == dict(prompt=PROMPT), 'raw JSON mode read draft settings'
                else:
                    assert api.requests[0]['n'] == 1 and api.requests[0]['quality'] == 'auto'
                result['exit_status'] = 1
            else:
                raise AssertionError(case)

            expected = 1 if case in ('auth-reopen', 'queued-enter', 'flags', 'json') else 0
            assert len(api.requests) == len(api.events) == expected, api.events
            assert all(event == dict(method='POST', path='/v1/images/generations') for event in api.events)
            assert not saved(home)
            for _, process in processes:
                assert KEY.encode() not in process.output
            (output / (case + '.state.json')).write_bytes(path.read_bytes())
            result.update(passed=True, image_posts=expected, automatic_checks=0, saved_images=0,
                          requests=api.requests, events=api.events,
                          process_exit_statuses=[p.status for _, p in processes])
            (output / (case + '.result.json')).write_text(json.dumps(result, indent=2) + '\n')
            return result
    finally:
        for label, process in processes:
            (output / (case + '.' + label + '.full.pty.bin')).write_bytes(process.output)
            process.close()
        (output / (case + '.requests.json')).write_text(json.dumps(api.requests, indent=2) + '\n')
        (output / (case + '.events.json')).write_text(json.dumps(api.events, indent=2) + '\n')
        api.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('output', type=pathlib.Path)
    parser.add_argument('--case', choices=CASES + ('auth-reopen',))
    parser.add_argument('--before', action='store_true')
    parser.add_argument('--scene', action='store_true')
    args = parser.parse_args()
    binary = args.binary.resolve()
    original = hashlib.sha256(binary.read_bytes()).hexdigest()
    if args.scene:
        fixture_label = 'Zero API requests' if args.case == 'unsent' else 'Synthetic HTTP401'
        print('\033[2J\033[H' + ('Before' if args.before else 'After') + ' | ' + fixture_label + ' | Native zsh PTY', flush=True)
        print('$ openai images generate', flush=True)
    results = []
    for case in ([args.case] if args.case else CASES):
        results.append(check(binary, args.output, case, args.before, args.scene))
        if not args.scene:
            print('PASS: ' + case, flush=True)
    assert original == hashlib.sha256(binary.read_bytes()).hexdigest()
    (args.output / 'results.json').write_text(json.dumps(dict(binary=str(binary), sha256=original, cases=results), indent=2) + '\n')
    if args.scene:
        time.sleep(0.5)


if __name__ == '__main__':
    main()
