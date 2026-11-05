#!/usr/bin/env python3
"""Exercise retained inline/expanded image states with synthetic loopback replies."""
import argparse
import fcntl
import hashlib
import json
import pathlib
import shlex
import struct
import tempfile
import termios
import sys

sys.path.insert(0,str(pathlib.Path(__file__).resolve().parent))
from image_picker_generation_check import SplitProcess, environment, fixture, saved, screen, PROMPT

def focus_images(process):
    process.send(b'\x1b')
    process.pump(0.15)
    process.send(b'\x1b[Z')
    process.pump(0.15)

def view_images(process):
    focus_images(process)
    process.send(b'\r')
BLOCK = b'\x1b[38;5;'


def run(binary, destination, narrow=False):
    destination.mkdir()
    api = fixture('history')
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='expanded-images-') as temporary:
            home = pathlib.Path(temporary) / ('h' * 80 if narrow else 'home')
            home.mkdir()
            env = environment(home, api)
            env.pop('NO_COLOR', None)
            env['COLORTERM'] = 'truecolor'
            executable = binary
            if narrow:
                executable = pathlib.Path(temporary) / 'history-wrapper'
                executable.write_text("#!/bin/sh\nprintf 'SYNTHETIC SHELL HISTORY\\nSYNTHETIC PRIOR OUTPUT\\n'\nexec " + shlex.quote(str(binary)) + ' "$@"\n')
                executable.chmod(0o700)
            process = SplitProcess(executable, env, stderr_pipe=True)
            width, height = (40, 12) if narrow else (96, 28)
            fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))

            def frame():
                return screen(bytes(process.streams['stdout']), width, height)

            def selected_caption(index):
                label = ('Image %d ·' % index).encode()
                current = frame()
                return ('› '.encode() + label in current or
                        any('╭'.encode() in line and label in line for line in current.splitlines()))

            process.ready()
            assert b'Expand images' not in frame() and b'Scroll images' not in frame()
            (destination / 'initial.stdout.bin').write_bytes(process.streams['stdout'])
            process.submit(PROMPT)
            process.wait_for(lambda: len(api.requests) == 1)
            api.gates[0].set()
            process.wait_for(lambda: len(saved(home)) == 8 and b'Saved in' in frame() and selected_caption(8) and b'Ctrl+C exit' in frame())
            process.pump(0.3)
            raw = bytes(process.streams['stdout'])
            assert BLOCK in raw
            assert b'Saved image:' not in raw, 'active panel repeated final receipts'
            focus_images(process)
            process.send(b'\x1b[C' * 7)
            process.wait_for(lambda: selected_caption(8))
            if narrow:
                history = []
                screen(raw, width, height, history)
                assert 'SYNTHETIC SHELL HISTORY' in '\n'.join(history), 'receipt wrapping lost prior shell output'
                assert b'Esc prompt' in frame()
                (destination / 'wrapped-receipts.stdout.bin').write_bytes(raw)
                width, height = 96, 28
                fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                process.wait_for(lambda: b'Command' in frame())
            else:
                process.send(b'\x1b[<64;10;23M')
                process.pump(0.2)
            focus_images(process)
            process.send(b'\x1b[F')
            process.wait_for(lambda: selected_caption(8))
            compact = frame()
            process.send(b'\r')
            process.wait_for(lambda: b'Esc back' in frame() and b'Create image' not in frame())
            process.send(b'\x1b[D')
            process.wait_for(lambda: 'Image 7 ·'.encode() in frame())
            process.send(b'\x1b[C')
            process.wait_for(lambda: 'Image 8 ·'.encode() in frame())
            process.send(b'\x1b[200~HIDDEN PASTE\x1b[201~\r\x07')
            process.pump(0.2)
            assert len(api.requests) == 1
            (destination / 'expanded.stdout.bin').write_bytes(process.streams['stdout'])
            process.send(b'\x1b')
            process.wait_for(lambda: b'Create image' in frame())
            (destination / 'compact-before.txt').write_bytes(compact)
            (destination / 'compact-after.txt').write_bytes(frame())
            assert frame() == compact, 'Back changed inline image scroll'
            process.send(b'\r\x07')
            process.pump(0.2)
            assert len(api.requests) == 1, 'queued submission escaped the view-return guard'
            if not narrow:
                process.pump(0.8)
                process.send(b'\x1b')
                process.pump(0.15)
                process.send(' second')
                process.pump(0.1)
                process.send(b'\r\r\x07')
                process.wait_for(lambda: len(api.requests) == 2)
                process.wait_for(lambda: b'Generating image' in frame())
                assert '▀'.encode() in frame(), 'new generation removed previous images'
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame())
                mark = len(process.streams['stdout'])
                api.gates[1].set()
                process.wait_for(lambda: len(saved(home)) == 16 and 'Image 8 ·'.encode() in frame() and b'Ctrl+C quit' in frame())
                process.pump(0.2)
                second_raw = bytes(process.streams['stdout'][mark:])
                assert b'Saved image:' not in second_raw
                assert len(api.requests) == 2
                process.send(b'\x1b')
                process.wait_for(lambda: PROMPT.encode() + b' second' in frame())
                assert b'HIDDEN PASTE' not in frame()
                process.pump(0.9)
                api.failed_requests.add(3)
                process.send(b'\x1b')
                process.pump(0.15)
                process.send(' failure')
                process.pump(0.1)
                process.send(b'\r')
                process.wait_for(lambda: len(api.requests) == 3)
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame())
                api.gates[2].set()
                process.wait_for(lambda: b'Request failed.' in frame())
                assert 'Image 8 ·'.encode() in frame() and len(saved(home)) == 16
                assert '▀'.encode() in frame(), 'failure removed the previous image'
                (destination / 'failed-expanded.stdout.bin').write_bytes(process.streams['stdout'])
                process.send(b'\x1b')
                process.wait_for(lambda: b'Create image' in frame() and b'Authentication failed' in frame())
                assert b'All images saved.' not in frame()
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame() and b'Request failed.' in frame())
                process.send(b'\x1b')
                process.wait_for(lambda: b'Create image' in frame())
                process.pump(0.9)
                process.send(b'\x1b')
                process.pump(0.15)
                process.send(' cancel')
                process.pump(0.1)
                process.send(b'\r')
                process.wait_for(lambda: len(api.requests) == 4)
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame())
            assert process.stop() == 130
            assert len(saved(home)) == (8 if narrow else 16)
            for path in saved(home):
                record = ('Saved image: \"' + str(home / path) + '\"').encode()
                assert process.streams['stdout'].count(record) == 1, 'exit lost or repeated a receipt'
            assert b'\x1b[2J' not in process.streams['stdout']
            assert b'\x1b[?1049' not in process.streams['stdout']
            restored = termios.tcgetattr(process.fd)[3]
            assert restored & (termios.ECHO | termios.ICANON) == termios.ECHO | termios.ICANON
            (destination / 'report.json').write_text(json.dumps(dict(passed=True, narrow=narrow, requests=len(api.requests), exit_status=process.status, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest()), indent=2) + '\n')
    finally:
        for gate in api.gates:
            gate.set()
        if process is not None:
            for name, data in process.streams.items():
                (destination / (name + '.bin')).write_bytes(data)
            process.close()
        api.close()


def run_print(binary, destination):
    destination.mkdir()
    api = fixture('success')
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='print-image-command-') as temporary:
            process = SplitProcess(binary, environment(pathlib.Path(temporary), api), stderr_pipe=True)
            process.ready()
            process.send('SYNTHETIC print-only')
            process.pump(0.2)
            process.send(b'\x10')
            process.wait_for(lambda: process.status is not None)
            raw = bytes(process.streams['stdout'])
            final = raw.rsplit(b'\x1b[?25h', 1)[-1]
            assert process.status == 0 and not api.requests
            assert b'openai images generate' in final and b'SYNTHETIC print-only' in final
            (destination / 'report.json').write_text(json.dumps(dict(passed=True, requests=0, exit_status=0)) + '\n')
    finally:
        if process is not None:
            for name, data in process.streams.items():
                (destination / (name + '.bin')).write_bytes(data)
            process.close()
        api.close()


def run_font_fallback(binary, destination, color=True):
    destination.mkdir()
    if sys.platform != 'darwin':
        (destination / 'report.json').write_text(json.dumps(dict(skipped='Apple Terminal protocol selection requires macOS')) + '\n')
        return
    from image_recovery_check import Fixture, preference
    api = Fixture(delay=0.1)
    process = None
    try:
        with tempfile.TemporaryDirectory(prefix='font-fallback-images-') as temporary:
            home = pathlib.Path(temporary)
            preference(home, '3')
            settings = home / 'Library/Application Support/openai/image-preferences.json'
            settings.write_text(json.dumps(dict(version=1, inline=True)))
            settings.chmod(0o600)
            env = environment(home, api)
            env['TERM_PROGRAM'] = 'Apple_Terminal'
            env['TERM_SESSION_ID'] = ''  # Deterministic FontError, before Automation or registration.
            if color:
                env.pop('NO_COLOR', None)
                env['COLORTERM'] = 'truecolor'
            process = SplitProcess(binary, env, stderr_pipe=True)
            fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', 256, 96, 0, 0))
            process.ready()
            def frame():
                return screen(bytes(process.streams['stdout']), 96, 256)
            for count, suffix in [(3, ' first'), (6, ' second')]:
                process.pump(0.9)
                process.send(b'\x1b')
                process.pump(0.15)
                process.send(suffix)
                process.pump(0.1)
                process.send(b'\r')
                process.wait_for(lambda: b'Saved in' in frame() and len(saved(home)) == count and b'Ctrl+C exit' in frame())
                assert len(api.requests) == count // 3
                if color:
                    assert '▀'.encode() in frame()
                    assert b'Color preview' in frame()
                else:
                    assert '▀'.encode() not in frame()
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame())
                process.pump(0.2)
                assert ('▀'.encode() in frame()) == color
                process.send(b'\x1b')
                process.wait_for(lambda: b'Create image' in frame())
            raw = bytes(process.streams['stdout'])
            if color:
                captions = [line for line in frame().splitlines() if b'Image ' in line and b'.png' in line]
                assert captions and all(line.count(b'Color preview') == 1 for line in captions), 'each visible image needs one preview-mode caption'
                assert frame().count(b'Color preview') == len(captions), 'preview labels escaped their image captions'
            else:
                assert frame().count(b'Sharp previews unavailable.') == 1, 'font warning repeated across files or requests'
            assert b'Sharp inline preview unavailable:' not in raw
            assert b'cannot be matched to the current session' not in raw
            assert process.stop() == 130
            assert len(saved(home)) == 6 and len(api.requests) == 2
            (destination / 'report.json').write_text(json.dumps(dict(passed=True, color=color, files=6, requests=2, exit_status=130, font_automation=False, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest()), indent=2) + '\n')
    finally:
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
    args.output.mkdir()
    run(args.binary.resolve(), args.output / 'retained-views')
    run(args.binary.resolve(), args.output / 'narrow-receipts', narrow=True)

    run_print(args.binary.resolve(), args.output / 'print-only')

    run_font_fallback(args.binary.resolve(), args.output / 'font-fallback')
    run_font_fallback(args.binary.resolve(), args.output / 'font-no-color', color=False)
