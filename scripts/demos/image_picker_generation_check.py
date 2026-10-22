#!/usr/bin/env python3
"""Check a fixed form and independently scrolling results against gated replies."""
import argparse
import fcntl
import hashlib
import json
import os
import pathlib
import re
import signal
import struct
import sys
import tempfile
import termios
import unicodedata

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from image_picker_loading_streams_check import SplitProcess, environment, fixture, saved

ANSI = re.compile(rb'\x1b\][^\x07]*?(?:\x07|\x1b\\)|\x1b_[\s\S]*?\x1b\\|\x1b\[[0-?]*[ -/]*[@-~]|\x1b[78]')
PROMPT = 'SYNTHETIC horse'


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

    for match in ANSI.finditer(raw):
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


def check(binary, destination, case):
    destination.mkdir(exist_ok=False)
    api = fixture('retry' if case == 'retry' else ('history' if case == 'history' else 'success'))
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='image-generation-form-') as directory:
            home = pathlib.Path(directory)
            process = SplitProcess(binary, environment(home, api), stderr_pipe=True)
            process.ready()
            initial = screen(bytes(process.streams['stdout']))
            assert b'Expand images' not in initial and b'Scroll images' not in initial
            assert '╭─ Images'.encode() not in initial, 'empty image pane before generation'
            before = len(process.streams['stdout'])
            process.submit(PROMPT)
            process.wait_for(lambda: len(api.requests) == 1)
            process.wait_for(lambda: b'1s elapsed' in process.streams['stdout'][before:])
            raw = bytes(process.streams['stdout'])
            frame = screen(raw)
            for marker in (b'Create image', PROMPT.encode(), b'Settings', b'Command', b'Generating image', b'Ctrl+C', b'cancel'):
                assert marker in frame, (marker, frame)
            image_top = next(i for i, line in enumerate(frame.splitlines()) if b'Generating image' in line)
            form_top = next(i for i, line in enumerate(frame.splitlines()) if b'Create image' in line)
            control_rows = image_top - form_top
            assert control_rows <= 19, 'full settings exceed the nineteen-row budget'
            pinned = frame.splitlines()[:image_top]
            first_spinner = frame.splitlines()[image_top].split(b'Generating image')[0]
            process.pump(0.2)
            animated = screen(bytes(process.streams['stdout']))
            assert animated.splitlines()[image_top].split(b'Generating image')[0] != first_spinner, 'spinner did not advance'
            assert animated.splitlines()[:image_top] == pinned, 'spinner moved the form'
            assert b'\x1b[?1049h' not in raw and b'\x1b[2J' not in raw, 'inline panel cleared or replaced the screen'
            assert b'[Enter]' not in frame, 'busy form must not advertise another request'
            assert b'Checking save folder' not in frame and b'Esc cancels' not in frame, 'busy form retained stale validation controls'
            assert PROMPT.encode() not in process.streams['stderr'], 'prompt entered diagnostics'
            (destination / 'pending.stdout.bin').write_bytes(raw)
            (destination / 'pending.screen.txt').write_bytes(frame)
            mark = len(process.streams['stdout'])
            process.wait_for(lambda: b'2s elapsed' in process.streams['stdout'][mark:])
            update = bytes(process.streams['stdout'][mark:])
            assert screen(bytes(process.streams['stdout'])).splitlines()[:image_top] == pinned, 'progress moved the controls'
            assert not any(marker in update for marker in (b'Create image', b'Settings', b'Command', b'\x1b[2J')), 'progress repainted the pinned form'
            assert len(update) < 4096, 'animated row updates unexpectedly repainted the screen'
            assert not api.gates[0].is_set(), 'request completed before the form observation'
            if case in ('cancel', 'term', 'hup'):
                if case == 'cancel':
                    assert process.stop() == 130
                else:
                    os.kill(process.pid, signal.SIGTERM if case == 'term' else signal.SIGHUP)
                    process.wait_for(lambda: process.status is not None)
                    assert process.status == (143 if case == 'term' else 129)
                assert not saved(home)
            else:
                fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 12, 40, 0, 0))
                mark = len(process.streams['stdout'])
                process.wait_for(lambda: b'Create image' in process.streams['stdout'][mark:])
                raw = bytes(process.streams['stdout'])
                frame = screen(raw, 40, 12)
                assert b'Ctrl+C' in frame and b'cancel' in frame
                fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 28, 96, 0, 0))
                api.gates[0].set()
                if case == 'retry':
                    process.wait_for(lambda: b'Authentication failed' in process.streams['stderr'])
                    process.pump(0.9)
                    process.send(' retry')
                    process.pump(0.15)
                    process.send(b'\r')
                    process.wait_for(lambda: len(api.requests) == 2)
                    mark = len(process.streams['stdout'])
                    process.wait_for(lambda: b'1s elapsed' in process.streams['stdout'][mark:])
                    api.gates[1].set()
                process.wait_for(lambda: b'Saved in' in screen(bytes(process.streams['stdout'])) and b'Ctrl+C exit' in screen(bytes(process.streams['stdout'])))
                complete = screen(bytes(process.streams['stdout']))
                form_top = next(i for i, line in enumerate(complete.splitlines()) if b'Create image' in line)
                image_top = form_top + control_rows
                assert b'Image' in complete.splitlines()[image_top+1], 'completion changed the relative results boundary'
                assert len(saved(home)) == (8 if case == 'history' else 1)
                process.pump(0.9)
                assert len(api.requests) == (2 if case == 'retry' else 1), 'reopening started another request'
                if case == 'history':
                    process.send(b'\x1b')
                    process.pump(0.15)
                    process.send(b'\x1b[Z')
                    process.pump(0.15)
                    process.send(b'\x1b[C' * 7)
                    process.wait_for(lambda: '› Image 8 ·'.encode() in screen(bytes(process.streams['stdout'])))
                    old = screen(bytes(process.streams['stdout']))
                    mark = len(process.streams['stdout'])
                    process.send(b'\x1b[D')
                    process.wait_for(lambda: '› Image 7 ·'.encode() in screen(bytes(process.streams['stdout'])))
                    process.pump(0.3)
                    new = screen(bytes(process.streams['stdout']))
                    assert old.splitlines()[:image_top] == new.splitlines()[:image_top], 'scrolling moved the pinned form'
                    assert old.splitlines()[image_top+1:27] != new.splitlines()[image_top+1:27], 'image navigation did not change the selected image'
                    assert b'Create image' not in process.streams['stdout'][mark:], 'scrolling repainted the form'
                    (destination / 'scrolled.stdout.bin').write_bytes(process.streams['stdout'])
                (destination / 'saved.stdout.bin').write_bytes(process.streams['stdout'])
                assert process.stop() == 130
                transcript = bytes(process.streams['stdout'])
                for path in saved(home):
                    assert str(path).encode() in transcript, 'exit lost a saved-file receipt'
            assert not api.errors, api.errors
            assert api.requests[0]['prompt'] == PROMPT
            if case == 'retry':
                assert api.requests[1]['prompt'] == PROMPT + ' retry'
            restored = termios.tcgetattr(process.fd)[3]
            assert restored & (termios.ECHO | termios.ICANON) == termios.ECHO | termios.ICANON, 'terminal input modes were not restored'
            result = dict(case=case, passed=True, requests=len(api.requests), exit_status=process.status,
                          binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
            (destination / 'report.json').write_text(json.dumps(result, indent=2) + '\n')
    finally:
        for gate in api.gates:
            gate.set()
        if process is not None:
            for name, data in process.streams.items():
                (destination / (name + '.bin')).write_bytes(data)
            process.close()
        api.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('output', type=pathlib.Path)
    args = parser.parse_args()
    args.output.mkdir(exist_ok=False)
    for case in ('success', 'retry', 'cancel', 'term', 'hup', 'history'):
        check(args.binary.resolve(), args.output / case, case)
