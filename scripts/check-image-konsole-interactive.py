#!/usr/bin/env python3
"""Exercise the interactive Konsole viewer through an isolated public CLI.

Run with a built binary and an evidence directory. These POSIX PTY checks prove
process and byte contracts, not native Konsole appearance or scrollback.
"""
import argparse
import base64
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import signal
import subprocess
import sys
import tempfile
import termios
import time

SCRIPTS = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
from image_picker_harness import Terminal

spec = importlib.util.spec_from_file_location('konsole_checks', SCRIPTS / 'check-image-konsole.py')
checks = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checks)
require = checks.require
ENTER, LEAVE = b'\x1b[?1049h', b'\x1b[?1049l'
FRAME = checks.IIP


def frames(raw):
    return FRAME.findall(raw)


def await_frame(terminal, after=0, timeout=10):
    deadline = time.monotonic() + timeout
    while not frames(terminal.raw[after:]):
        terminal.read()
        require(terminal.child.poll() is None, 'viewer exited before its frame')
        require(time.monotonic() < deadline, 'viewer did not finish a frame')
    return frames(terminal.raw[after:])[-1]


def width(frame):
    header, _ = frame
    return int(dict(field.split(b'=', 1) for field in header.split(b';'))[b'width'])


def finish(terminal, expected, timeout=6, entered=True):
    deadline = time.monotonic() + timeout
    while terminal.child.poll() is None:
        terminal.read()
        require(time.monotonic() < deadline, 'viewer did not exit promptly')
    # Linux PTYs can return only one small queue segment per read. Drain until
    # quiet so a completed child cannot leave recovery controls unobserved.
    drain_deadline = time.monotonic() + 2
    while True:
        previous = len(terminal.raw)
        terminal.read(.05)
        if len(terminal.raw) == previous:
            break
        require(time.monotonic() < drain_deadline, 'output did not become quiet after exit')
    require(terminal.child.returncode == expected,
            f'wrong exit {terminal.child.returncode}, expected {expected}: {terminal.text()[-500:]}')
    restored = termios.tcgetattr(terminal.slave)
    initial = list(terminal.initial)
    if sys.platform == 'darwin':
        restored[3] &= ~termios.PENDIN
        initial[3] &= ~termios.PENDIN
    require(restored == initial, 'terminal input/output modes were not restored')
    raw = bytes(terminal.raw)
    require(b'\x1b[3J' not in raw, 'viewer cleared terminal history')
    if entered:
        require(raw.count(ENTER) == 1 and raw.count(LEAVE) == 1, 'alternate screen was not balanced')
        require(raw.index(ENTER) < raw.index(LEAVE), 'alternate screen order changed')
        require(raw.rfind(b'\x1b[?25r') > raw.rfind(b'\x1b[?25l'), 'cursor was not restored')
        require(raw.index(b'\x1b[?25s') < raw.index(b'\x1b[?25l'), 'cursor state was not saved')
        require(raw.index(b'\x1b[?2004s') < raw.index(b'\x1b[?2004h'), 'paste state was not saved')
        require(raw.rfind(b'\x1b[?2004r') > raw.rfind(b'\x1b[?2004h'), 'paste state was not restored')
        for match in re.finditer(rb'\x1b\[[02]?J', raw):
            require(raw.index(ENTER) < match.start() < raw.index(LEAVE), 'clear escaped the alternate screen')
    elif entered is False:
        require(ENTER not in raw and LEAVE not in raw, 'rejected command changed the screen')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('output', type=pathlib.Path)
    parser.add_argument('--case', help='Run only this named case.')
    args = parser.parse_args()
    binary, output = args.binary.resolve(), args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    evidence = dict(binary=str(binary), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                    checker_sha256=hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                    scope='Private POSIX PTYs; synthetic terminal identity; no native appearance claim.',
                    results=[])
    normal, pixels = checks.fixtures.fixture_png(32, 16)
    with tempfile.TemporaryDirectory(prefix='konsole-interactive-check-') as temporary:
        root = pathlib.Path(temporary)
        source = root / 'local synthetic image.png'
        source.write_bytes(normal)
        env = dict(PATH='/usr/bin:/bin', HOME=str(root), XDG_CONFIG_HOME=str(root / 'config'),
                   LANG='C.UTF-8', TERM='xterm-256color', TERM_PROGRAM='', KONSOLE_VERSION='230805',
                   OPENAI_BASE_URL='http://127.0.0.1:1/v1', OPENAI_API_KEY='synthetic-unused-key')

        def run(name, interact=None, *, expected=0, extra=None, image=normal, entered=True,
                extra_args=(), preview_args=(), stdin=None, stdout=None, stderr_tty=False, geometry=(80, 24)):
            if args.case and args.case != name:
                return
            source.write_bytes(image)
            current = dict(env, **(extra or {}))
            error_path = output / (name + '.stderr')
            arguments = [*extra_args, 'images', 'preview', '--interactive', *preview_args, str(source)]
            with error_path.open('wb') as stderr:
                terminal = Terminal(str(binary), arguments, current, width=geometry[0], height=geometry[1],
                                    input_file=stdin, output_file=stdout,
                                    stderr_file=None if stderr_tty else stderr)
            detail = {}
            try:
                if interact:
                    detail = interact(terminal) or {}
                finish(terminal, expected, entered=entered)
                if entered and image == normal:
                    decoded = [checks.fixtures.decode_png(base64.b64decode(payload, validate=True))
                               for _, payload in frames(terminal.raw)]
                    require(decoded and all(d == (32, 16, pixels) for d in decoded), 'image pixels changed')
                if not detail.get('replaced_source'):
                    require(source.read_bytes() == image, 'viewer changed the original image')
                if expected == 0:
                    require(not error_path.read_bytes(), 'successful viewer emitted an error')
                else:
                    require(b'\x1b]1337;' not in error_path.read_bytes(), 'graphics leaked into stderr')
                row = dict(case=name, result='pass', exit=expected, frames=len(frames(terminal.raw)),
                           elapsed_seconds=round(time.monotonic() - terminal.started, 3), **detail)
                evidence['results'].append(row)
                print('PASS', name, flush=True)
            except Exception as error:
                evidence['results'].append(dict(case=name, result='fail', error=str(error)))
                raise
            finally:
                try:
                    terminal.save(output / name)
                finally:
                    terminal.close()

        def close_key(key):
            def action(t):
                await_frame(t)
                t.send(key)
            return action

        def close_signal(number, group=False):
            def action(t):
                await_frame(t)
                if group:
                    os.killpg(t.child.pid, number)
                else:
                    t.child.send_signal(number)
            return action

        def resizing(t):
            first = await_frame(t)
            initial_width = width(first)
            sizes = [(35, 18), (120, 45), (2, 2), (80, 24)]
            measured = []
            for columns, rows in sizes:
                offset = len(t.raw)
                t.resize(columns, rows)
                if columns == 2:
                    deadline = time.monotonic() + .4
                    while time.monotonic() < deadline:
                        t.read()
                    require(t.child.poll() is None, 'tiny size killed viewer')
                else:
                    frame = await_frame(t, offset)
                    require(0 < width(frame) < columns, 'redraw exceeds viewport width')
                    measured.append(width(frame))
            require(measured[0] < initial_width and measured[-1] == initial_width,
                    'narrowing/restoration did not change and restore size')
            t.send(b'q')
            return dict(resize_widths=measured)

        def rapid_resize(t):
            await_frame(t)
            for cycle in range(30):
                t.resize(35 + cycle % 4, 15 + cycle % 3)
            t.resize(31, 17)
            offset = len(t.raw)
            await_frame(t, offset)
            deadline = time.monotonic() + .5
            while time.monotonic() < deadline:
                t.read()
            last = frames(t.raw)[-1]
            require(width(last) < 31, 'resize burst did not converge to final size')
            require(t.child.poll() is None, 'resize burst terminated viewer')
            t.send(b'q')
            return dict(resize_events=31, final_width=width(last))

        def keys_and_paste(t):
            await_frame(t)
            t.send(b'\x1b[A\x1b[B\x1b[C\x1b[D')
            t.send(b'\x1b[200~q\x1b\x03not-a-command\x1b[201~')
            deadline = time.monotonic() + .4
            while time.monotonic() < deadline:
                t.read()
            require(t.child.poll() is None, 'arrow or pasted escape/control text closed viewer')
            t.send(b'q')

        def replaced_image(t):
            await_frame(t)
            source.unlink()
            source.symlink_to(root / 'missing replacement')
            offset = len(t.raw)
            t.resize(40, 18)
            await_frame(t, offset)
            require(source.is_symlink(), 'viewer modified replacement')
            source.unlink()
            source.write_bytes(normal)
            t.send(b'q')
            return dict(replaced_source=True)

        def blocked(number=None):
            def action(t):
                deadline = time.monotonic() + 12
                while b'\x1b]1337;File=' not in t.raw or len(t.raw) < 4096:
                    t.read()
                    require(t.child.poll() is None and time.monotonic() < deadline, 'large frame did not start')
                suspended = False
                try:
                    termios.tcflow(t.slave, termios.TCOOFF)
                    suspended = True
                    started = time.monotonic()
                    if number is None:
                        t.send(b'q')
                    else:
                        t.child.send_signal(number)
                    status = t.child.wait(timeout=5)
                    expected = 1 if number is None else 128 + number
                    require(status == expected, 'blocked cancellation lost failure or signal status')
                    return dict(blocked_cancellation_seconds=round(time.monotonic()-started, 3),
                                output_suspended_until_exit=True, actual_blocked_exit=status)
                finally:
                    if suspended:
                        termios.tcflow(t.slave, termios.TCOON)
            return action

        def quit_during_frame(key):
            def action(t):
                deadline = time.monotonic() + 12
                while b'\x1b]1337;File=' not in t.raw or len(t.raw) < 4096:
                    t.read()
                    require(t.child.poll() is None and time.monotonic() < deadline, 'large frame did not start')
                prefix = bytes(t.raw)
                start = prefix.rfind(b'\x1b]1337;File=')
                require(b'\x07' not in prefix[start:] and b'\x1b\\' not in prefix[start:],
                        'frame finished before cancellation')
                t.send(key)
                # Keep backpressure while standalone Escape is disambiguated.
                time.sleep(.2)
                return dict(quit_during_incomplete_frame=True)
            return action

        try:
            run('quit', close_key(b'q'))
            run('escape', close_key(b'\x1b'))
            run('control-c', close_key(b'\x03'), expected=130)
            run('signal-int', close_signal(signal.SIGINT), expected=130)
            run('signal-term', close_signal(signal.SIGTERM), expected=143)
            run('signal-hup', close_signal(signal.SIGHUP), expected=129)
            run('group-int', close_signal(signal.SIGINT, True), expected=130)
            run('group-term', close_signal(signal.SIGTERM, True), expected=143)
            run('group-hup', close_signal(signal.SIGHUP, True), expected=129)
            run('resize-and-restore', resizing)
            run('resize-burst', rapid_resize)
            run('arrows-and-paste', keys_and_paste)
            run('replaced-source', replaced_image)
            run('no-color', close_key(b'q'), extra={'NO_COLOR': '1'})
            run('old-protocol-floor', close_key(b'q'), extra={'KONSOLE_VERSION': '220400'})
            for index in range(5):
                run('repeat-' + str(index), close_key(b'q'))
            for name, extra in [
                ('ci', {'CI':'true'}), ('tmux', {'TMUX':'active'}), ('screen', {'STY':'active'}),
                ('zellij', {'ZELLIJ':'active'}), ('dumb', {'TERM':'dumb'}),
                ('older-konsole', {'KONSOLE_VERSION':'220399'}),
                ('unknown-konsole', {'KONSOLE_VERSION':''}), ('conflicting-terminal', {'TERM_PROGRAM':'vscode'}),
            ]:
                run('reject-' + name, expected=1, extra=extra, entered=False)
            for name, options in [('json', ('--format','json')), ('extract', ('--transform','id')),
                                  ('raw', ('--raw-output',))]:
                run('reject-' + name, expected=1, extra_args=options, entered=False)
            run('reject-piped-input', expected=1, stdin=subprocess.DEVNULL, entered=False)
            run('reject-redirected-output', expected=1, stdout=subprocess.DEVNULL, entered=False)
            run('reject-inline-auto', expected=1, preview_args=('--inline', 'auto'), entered=False)
            run('reject-inline-on', expected=1, preview_args=('--inline', 'on'), entered=False)
            run('reject-malformed', expected=1, image=b'not an image', entered=False)
            # Blocked-output cases may intentionally be unable to restore escape
            # controls. Run separately and inspect terminal-mode restoration.
            run('blocked-int', blocked(signal.SIGINT), expected=130, image=checks.large_png(), entered=None)
            for name, number, status in [('int', signal.SIGINT, 130), ('term', signal.SIGTERM, 143), ('quit', None, 1)]:
                run('blocked-shared-' + name, blocked(number), expected=status,
                    image=checks.large_png(), entered=None, stderr_tty=True)
            run('quit-during-frame', quit_during_frame(b'q'), image=checks.large_png())
            run('escape-during-frame', quit_during_frame(b'\x1b'), image=checks.large_png())
        finally:
            (output / 'results.json').write_text(json.dumps(evidence, indent=2) + '\n')
    require(evidence['results'], 'no selected cases executed')


if __name__ == '__main__':
    main()
