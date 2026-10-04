#!/usr/bin/env python3
"""Check VS Code image opt-in through the built CLI, offline.

    python3 -I -B scripts/check-image-vscode.py OPENAI_BINARY OUTPUT_DIR [--baseline BASELINE_BINARY]

Uses sized POSIX PTYs and synthetic loopback responses. This checks protocol,
input, saving and process contracts, not native VS Code rendering on any OS.
The PTYs report zero pixel dimensions, including after resizing; that exercises
missing cell metadata but does not constitute native Windows execution.
"""
import argparse
import base64
import binascii
import hashlib
import http.server
import json
import os
import pathlib
import platform
import re
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import threading
import time
import zlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from image_picker_harness import Terminal, generate as submit_picker, ready, wait_for_request


class CapturedTerminal(Terminal):
    """Use the shared PTY lifecycle with a separate real stderr descriptor."""
    def __init__(self, binary, args, env, stderr, width, height, input_file=None):
        super().__init__(str(binary), args, env, width=width, height=height,
                         input_file=input_file, stderr_file=stderr)


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def fixture_png(width, height):
    pixels = bytes(channel for y in range(height) for x in range(width)
                   for channel in ((x * 71) % 256, (y * 43) % 256, 173, 127 if x % 2 else 255))

    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', binascii.crc32(kind + data) & 0xffffffff)

    rows = b''.join(b'\0' + pixels[y * width * 4:(y + 1) * width * 4] for y in range(height))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', width, height, 8, 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b'')), pixels


def decode_png(data):
    """Decode the RGBA8 fixtures after Go's PNG encoder changes row filters."""
    require(data.startswith(b'\x89PNG\r\n\x1a\n'), 'preview is not a PNG')
    offset, compressed, shape, ended = 8, bytearray(), None, False
    while offset < len(data):
        require(offset + 12 <= len(data), 'truncated PNG chunk')
        length = struct.unpack('>I', data[offset:offset + 4])[0]
        kind = data[offset + 4:offset + 8]
        end = offset + 8 + length
        require(end + 4 <= len(data), 'truncated PNG payload')
        payload = data[offset + 8:end]
        require(struct.unpack('>I', data[end:end + 4])[0] == binascii.crc32(kind + payload) & 0xffffffff,
                'PNG checksum mismatch')
        if kind == b'IHDR':
            require(shape is None and offset == 8, 'unexpected PNG header')
            shape = struct.unpack('>IIBBBBB', payload)
            require(shape[2:] == (8, 6, 0, 0, 0), 'expected RGBA8 fixture')
        elif kind == b'IDAT':
            compressed.extend(payload)
        elif kind == b'IEND':
            require(not payload and end + 4 == len(data), 'invalid PNG ending')
            ended = True
        offset = end + 4
    require(shape is not None and ended, 'incomplete PNG')
    width, height = shape[:2]
    stride = width * 4
    rows = zlib.decompress(compressed)
    require(len(rows) == (stride + 1) * height, 'wrong decoded PNG size')
    previous, pixels = bytearray(stride), bytearray()
    for y in range(height):
        start = y * (stride + 1)
        kind, row = rows[start], bytearray(rows[start + 1:start + 1 + stride])
        require(kind <= 4, 'unknown PNG filter')
        for x in range(stride):
            left, up, corner = row[x - 4] if x >= 4 else 0, previous[x], previous[x - 4] if x >= 4 else 0
            paeth = left + up - corner
            distances = (abs(paeth - left), abs(paeth - up), abs(paeth - corner))
            prediction = (0, left, up, (left + up) // 2, (left, up, corner)[distances.index(min(distances))])[kind]
            row[x] = (row[x] + prediction) & 255
        pixels.extend(row)
        previous = row
    return width, height, bytes(pixels)


def check_frames(raw, count, fixture, picker=False):
    frames = re.findall(rb'\x1b\]1337;File=([^:]*):([^\x07\x1b]*)(?:\x07|\x1b\\)', raw)
    require(len(frames) == count and raw.count(b'\x1b]1337;') == count,
            f'expected {count} complete image frames, got {len(frames)}')
    for header, payload in frames:
        fields = dict(field.split(b'=', 1) for field in header.split(b';'))
        require(all(fields.get(key) == value for key, value in
                    {b'inline': b'1', b'width': b'auto', b'height': b'auto'}.items()), f'wrong sizing: {header!r}')
        image = base64.b64decode(payload, validate=True)
        require(fields.get(b'size') == str(len(image)).encode(), 'image size header does not match payload')
        require(decode_png(image) == fixture, 'preview changed pixels, alpha or dimensions')
    # The picker's existing single input owner issues UI capability queries.
    # Direct commands must not solicit replies. Neither may delete old images.
    forbidden = (b'\x1b_G', b'1337;Clear')
    if not picker:
        forbidden += (b'\x1b[6n', b'\x1b[c', b'\x1b[0c', b'\x1b]11;')
    for query in forbidden:
        require(query not in raw, f'unexpected terminal query or graphics command: {query!r}')


class Response:
    def __init__(self, mode, image, gated=False):
        self.mode, self.image, self.gated = mode, image, gated
        self.started, self.release = threading.Event(), threading.Event()
        self.requests, self.errors = [], []


class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        case = self.server.case
        try:
            require(self.path == '/v1/images/generations', f'unexpected API path: {self.path}')
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            case.requests.append(body)
            case.started.set()
            encoded = base64.b64encode(case.image).decode()
            if case.mode.startswith('stream'):
                self.send_response(200)
                self.send_header('Content-Type', 'text/event-stream')
                self.end_headers()

                def event(value):
                    self.wfile.write(b'data: ' + json.dumps(value).encode() + b'\n\n')
                    self.wfile.flush()

                event({'type': 'image_generation.partial_image', 'partial_image_index': 0, 'b64_json': encoded})
                if case.gated:
                    require(case.release.wait(12), 'first progress image did not release fixture')
                if case.mode == 'stream-error':
                    event({'type': 'error', 'message': 'Synthetic stream failure'})
                elif case.mode == 'stream-malformed':
                    self.wfile.write(b'data: {broken\n\n')
                elif case.mode != 'stream-eof':
                    # Duplicate partials must not create repeated placements.
                    event({'type': 'image_generation.partial_image', 'partial_image_index': 0, 'b64_json': encoded})
                    event({'type': 'image_generation.partial_image', 'partial_image_index': 1, 'b64_json': encoded})
                    event({'type': 'image_generation.completed', 'b64_json': encoded})
                return
            if case.gated:
                require(case.release.wait(12), 'delayed fixture was not released')
            status = 400 if case.mode == 'error' else 200
            value = ({'error': {'message': 'Synthetic API failure', 'type': 'invalid_request_error'}} if status != 200
                     else {'created': 1700000000, 'data': [{'b64_json': encoded}]})
            if case.mode == 'partial-save-error':
                value['data'].append({'b64_json': 'invalid-synthetic-image'})
            data = json.dumps(value).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(data)))
            self.end_headers()
            self.wfile.write(data)
        except (BrokenPipeError, ConnectionResetError):
            pass  # Cancellation deliberately closes the response.
        except Exception as error:
            case.errors.append(str(error))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    parser.add_argument('--baseline', help='Optionally compare the same opt-in against the main baseline binary')
    args = parser.parse_args()
    binary = pathlib.Path(args.binary).resolve()
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    result_path = output / 'results.json'
    result_path.unlink(missing_ok=True)
    results = []
    normal, normal_pixels = fixture_png(4, 3)
    tall, tall_pixels = fixture_png(2, 512)
    normal_shape, tall_shape = (4, 3, normal_pixels), (2, 512, tall_pixels)
    with tempfile.TemporaryDirectory(prefix='vscode-images-') as temporary:
        root = pathlib.Path(temporary)
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base_env = {'PATH': '/usr/bin:/bin', 'LANG': 'en_US.UTF-8', 'TERM': 'xterm-256color',
                    'TERM_PROGRAM': 'vscode', 'OPENAI_VSCODE_IMAGES': '1',
                    'OPENAI_API_KEY': 'synthetic-vscode-key',
                    'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1'}

        def environment(name, extra=None):
            home = root / name
            home.mkdir(exist_ok=True)
            env = dict(base_env, HOME=str(home), XDG_CONFIG_HOME=str(home / 'config'), APPDATA=str(home / 'config'))
            env.update(extra or {})
            return home, env

        def passed(name, **details):
            results.append(dict(case=name, result='pass', **details))
            print('PASS', name, flush=True)

        def files(home):
            return list((home / 'Downloads' / 'gpt-images').glob('*.png'))

        def check_saved(home, image, count=1):
            saved = files(home)
            require(len(saved) == count and all(path.read_bytes() == image for path in saved),
                    f'wrong saved originals: {[path.name for path in saved]}')

        def tty(name, arguments, case=None, extra=None, expected=0, images=1, shape=normal_shape,
                width=80, height=24, interact=None, home_name=None, input_data=None, saved=1, stderr_tty=False,
                executable=None):
            executable = executable or binary
            home, env = environment(home_name or name, extra)
            server.case = case or Response('ok', normal)
            case = server.case
            stderr_path = output / (name + '.stderr')
            with stderr_path.open('wb') as stderr_file:
                if stderr_tty:
                    terminal = Terminal(str(executable), arguments, env, width=width, height=height)
                else:
                    terminal = CapturedTerminal(executable, arguments, env, stderr_file, width, height,
                                                input_file=subprocess.PIPE if input_data is not None else None)
            try:
                if input_data is not None:
                    terminal.child.stdin.write(input_data)
                    terminal.child.stdin.close()
                if interact:
                    interact(terminal, case)
                terminal.finish(expected, expect_picker=False)
                error = stderr_path.read_bytes()
                check_frames(bytes(terminal.raw), images, shape)
                require(not case.errors, case.errors)
                require(len(case.requests) == int('generate' in arguments), 'wrong number of API requests')
                if '--partial-images' in arguments:
                    require(case.requests[0].get('stream') is True and case.requests[0].get('partial_images') == 2,
                            'progress request fields changed')
                if expected == 0:
                    require(not error, f'unexpected stderr: {error!r}')
                elif not stderr_tty:
                    require(error and b'\x1b]1337;' not in error, f'error missing from stderr: {error!r}')
                if expected == 130:
                    require(b'cancel' in terminal.raw.lower(), 'cancellation diagnostic missing')
                    require(time.monotonic() - terminal.started < 5, 'cancellation did not complete promptly')
                if saved is not None:
                    if saved:
                        check_saved(home, case.image, saved)
                    else:
                        require(not files(home), 'unexpected saved image')
                passed(name, exit=expected, frames=images, stderr='pty' if stderr_tty else 'separate',
                       seconds=round(time.monotonic() - terminal.started, 3))
                return home, case, bytes(terminal.raw), error
            finally:
                case.release.set()
                try:
                    terminal.save(output / name)
                finally:
                    terminal.close()

        def pipe(name, arguments, expected=0, mode='ok', input_data=b''):
            home, env = environment(name)
            server.case = Response(mode, normal)
            result = subprocess.run([str(binary), *arguments], input=input_data, capture_output=True, env=env, timeout=12)
            (output / (name + '.stdout')).write_bytes(result.stdout)
            (output / (name + '.stderr')).write_bytes(result.stderr)
            require(result.returncode == expected, (name, result.returncode, result.stderr))
            require(b'\x1b' not in result.stdout + result.stderr, 'terminal controls in redirected output')
            require(not server.case.errors, server.case.errors)
            return home, result

        def repeated_picker(replace):
            name = 'picker-repeat-executable-replaced' if replace else 'picker-repeat'
            home, env = environment(name, {'OPENAI_PICKER_SHELL': 'bash', 'SHELL': '/bin/bash'})
            executable = binary
            if replace:
                executable = home / 'openai'
                shutil.copy2(binary, executable)
            server.case = case = Response('ok', normal)
            terminal = Terminal(str(executable), ['images', 'generate'], env, width=120, height=32)
            prompts = ['Synthetic first picker image', 'Synthetic second picker image']
            try:
                ready(terminal)
                for index, prompt in enumerate(prompts):
                    mark = len(terminal.raw)
                    case.started.clear()
                    terminal.send(b'\x1b[200~' + prompt.encode() + b'\x1b[201~')
                    submit_picker(terminal)
                    wait_for_request(terminal, case.started)
                    terminal.wait('Saved image:', after=mark)
                    saved = terminal.raw.index(b'Saved image:', mark)
                    terminal.wait('Images', after=saved)
                    terminal.wait('Describe your image', after=saved)
                    check_frames(bytes(terminal.raw), index + 1, normal_shape, picker=True)
                    check_saved(home, normal, index + 1)
                    require(len(case.requests) == index + 1, 'picker submitted an extra request')
                    require(case.requests[-1]['prompt'] == prompt, 'picker changed request prompt')
                    if index == 0:
                        if replace:
                            executable.rename(home / 'original executable')
                            executable.write_text('#!/bin/sh\nexit 23\n')
                            executable.chmod(0o700)
                        # Match the shared picker regression's quiet-input guard,
                        # then prove the returned prompt is blank and accepting
                        # input before submitting the next generation.
                        until = time.monotonic() + 1
                        while time.monotonic() < until:
                            terminal.read(0.02)
                        blank = len(terminal.raw)
                        submit_picker(terminal)
                        terminal.wait('Add a prompt first.', after=blank)
                        require(len(case.requests) == 1, 'blank resumed picker generated an image')
                terminal.send(b'\x03')
                terminal.finish(130)
                check_frames(bytes(terminal.raw), 2, normal_shape, picker=True)
                require(not case.errors, case.errors)
                require(len(case.requests) == 2, 'Ctrl+C submitted another request')
                check_saved(home, normal, 2)
                passed(name, exit=130, frames=2, requests=2, executable_replaced=replace)
            finally:
                try:
                    terminal.save(output / name)
                finally:
                    terminal.close()

        generate = ['images', 'generate', '--prompt', 'Synthetic VS Code fixture']
        stream = [*generate, '--partial-images', '2', '--max-items', '-1']
        try:
            if args.baseline:
                _, _, raw, _ = tty('baseline-opt-in-unrecognized', generate, images=0,
                                   executable=pathlib.Path(args.baseline).resolve())
                require('▀'.encode() in raw, 'baseline did not use its existing color-block fallback')
            for name, extra, flags, frames in [
                    ('enabled-auto', {}, [], 1), ('enabled-on', {}, ['--inline', 'on'], 1),
                    ('enabled-no-color', {'NO_COLOR': '1'}, [], 1),
                    ('explicit-off', {}, ['--inline', 'off'], 0),
                    ('unknown-color', {'OPENAI_VSCODE_IMAGES': ''}, [], 0),
                    ('disabled-no-color', {'OPENAI_VSCODE_IMAGES': '0', 'NO_COLOR': '1'}, [], 0),
                    ('unknown-on-no-color', {'OPENAI_VSCODE_IMAGES': '', 'NO_COLOR': '1'}, ['--inline', 'on'], 0),
                    ('invalid-opt-in', {'OPENAI_VSCODE_IMAGES': 'true', 'NO_COLOR': '1'}, [], 0),
                    ('non-vscode', {'TERM_PROGRAM': 'unknown', 'NO_COLOR': '1'}, [], 0),
                    ('ci', {'CI': 'true'}, [], 0), ('dumb', {'TERM': 'dumb'}, [], 0),
                    ('tmux', {'TMUX': 'synthetic', 'NO_COLOR': '1'}, [], 0),
                    ('screen', {'STY': 'synthetic', 'NO_COLOR': '1'}, [], 0),
                    ('zellij', {'ZELLIJ': 'synthetic', 'NO_COLOR': '1'}, [], 0),
                    ('term-screen', {'TERM': 'screen-256color', 'NO_COLOR': '1'}, [], 0),
                    ('term-tmux', {'TERM': 'tmux-256color', 'NO_COLOR': '1'}, [], 0),
                    ('remote-unknown', {'SSH_CONNECTION': 'synthetic', 'OPENAI_VSCODE_IMAGES': '', 'NO_COLOR': '1'}, [], 0),
                    ('remote-explicit', {'SSH_CONNECTION': 'synthetic'}, [], 1)]:
                _, _, raw, _ = tty(name, generate + flags, extra=extra, images=frames)
                if name == 'unknown-color':
                    require('▀'.encode() in raw, 'unknown capability lost the existing color-block fallback')

            tty('remember-off', ['images', 'inline', 'off'], home_name='preferences', images=0, saved=0)
            tty('persisted-off', generate, home_name='preferences', images=0)
            tty('override-off-with-on', generate + ['--inline', 'on'], home_name='preferences', saved=2)
            tty('override-off-with-auto', generate + ['--inline', 'auto'], home_name='preferences', saved=3)

            source = root / 'fixture with spaces.png'
            source.write_bytes(normal)
            tty('local-preview', ['images', 'preview', str(source)], saved=0)
            require(source.read_bytes() == normal, 'preview modified original')
            tall_source = root / 'tall.png'
            tall_source.write_bytes(tall)
            tty('tall-preview', ['images', 'preview', str(tall_source)], shape=tall_shape, width=18, height=8, saved=0)

            def release_after_resize(terminal, case):
                wait_for_request(terminal, case.started)
                terminal.resize(17, 7)
                case.release.set()

            tty('tall-save-resized', generate, case=Response('ok', tall, True), shape=tall_shape,
                interact=release_after_resize)

            def queued_input(terminal, case):
                wait_for_request(terminal, case.started)
                queued = b'echo synthetic-next-command\n'
                terminal.send(queued)
                case.release.set()
                terminal.finish(0, expect_picker=False)
                require(select.select([terminal.slave], [], [], 1)[0], 'queued input was consumed')
                require(os.read(terminal.slave, 4096) == queued, 'queued input changed')

            tty('queued-input-preserved', generate, case=Response('ok', normal, True), interact=queued_input)
            _, case, _, _ = tty('piped-json-stdin', ['images', 'generate'],
                                input_data=b'{"prompt":"Synthetic piped input","n":1,"metadata":{"fixture":true}}')
            require(case.requests[0]['prompt'] == 'Synthetic piped input' and
                    case.requests[0]['metadata'] == {'fixture': True}, 'piped JSON request changed')

            for iteration in range(2):
                tty(f'repeat-{iteration + 1}', generate, home_name='repeat', saved=iteration + 1)
            tty('repeat-error-preserves-originals', generate, case=Response('error', normal),
                home_name='repeat', saved=2, expected=1, images=0)
            _, _, raw, error = tty('partial-save-error', generate + ['--count', '2'],
                                   case=Response('partial-save-error', normal), expected=1, images=0)
            require(b'Saved image:' in raw and b'Saved 1 image(s)' in error and b'listed files are kept' in error,
                    'partial save error lost preserved-file guidance')

            def first_before_final(terminal, case):
                terminal.wait('Progress preview 1 of 2:')
                deadline = time.monotonic() + 8
                while b'\x07' not in terminal.raw:
                    require(time.monotonic() < deadline, 'partial image was buffered until final')
                    terminal.read()
                check_frames(bytes(terminal.raw), 1, normal_shape)
                terminal.resize(19, 9)
                case.release.set()

            tty('progress-final-resized', stream, case=Response('stream-ok', normal, True),
                interact=first_before_final, images=3)
            for mode in ('stream-error', 'stream-eof', 'stream-malformed'):
                tty(mode, stream, case=Response(mode, normal, True), interact=first_before_final,
                    expected=1, images=1, saved=0)

            def interrupt_after_request(terminal, case):
                wait_for_request(terminal, case.started)
                terminal.child.send_signal(signal.SIGINT)

            tty('cancel-idle', generate, case=Response('ok', normal, True), interact=interrupt_after_request,
                expected=130, images=0, saved=0, stderr_tty=True)

            def interrupt_after_partial(terminal, case):
                terminal.wait('Progress preview 1 of 2:')
                deadline = time.monotonic() + 8
                while b'\x07' not in terminal.raw:
                    require(time.monotonic() < deadline, 'partial image did not arrive')
                    terminal.read()
                terminal.child.send_signal(signal.SIGINT)

            tty('cancel-after-partial', stream, case=Response('stream-ok', normal, True),
                interact=interrupt_after_partial, expected=130, images=1, saved=0, stderr_tty=True)
            _, _, _, error = tty('api-error-tty', generate, case=Response('error', normal), expected=1, images=0, saved=0)
            require(b'400 Bad Request' in error and b'--format-error json' in error, 'readable API error lost')

            tty('format-text-saving', ['--format', 'text', *generate])
            for name, flags in [('format-json', ['--format', 'json']), ('format-jsonl', ['--format', 'jsonl']),
                                ('raw-output', ['--raw-output']), ('transform', ['--transform', 'data.0.b64_json']),
                                ('extract', ['--format', 'text', '--transform', 'data.0.b64_json'])]:
                _, _, raw, _ = tty(name, flags + generate, images=0, saved=0)
                require(base64.b64encode(normal) in raw, f'{name} lost explicit API payload')
                require(b'Saved image:' not in raw, 'explicit API mode unexpectedly saved image')

            home, result = pipe('stdout-pipe', generate)
            check_saved(home, normal)
            require(b'Saved image:' in result.stdout and not result.stderr, 'redirected save result changed')
            passed('stdout-pipe', exit=0, frames=0)
            _, result = pipe('structured-api-error', ['--format-error', 'json', *generate], expected=1, mode='error')
            require(json.loads(result.stderr) and b'Synthetic API failure' in result.stderr and not result.stdout,
                    'structured error is not one JSON document with API details')
            passed('structured-api-error', exit=1, frames=0)
            _, result = pipe('preview-pipe-rejected', ['images', 'preview', str(source)], expected=1)
            require(b'require a terminal' in result.stderr and not result.stdout, 'piped preview diagnostic changed')
            passed('preview-pipe-rejected', exit=1, frames=0)
            repeated_picker(False)
            repeated_picker(True)
        finally:
            server.case.release.set()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)
    evidence = {'binary': str(binary), 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                'checker_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                'host': {'system': platform.system(), 'machine': platform.machine(), 'python': platform.python_version()},
                'scope': 'Built CLI with POSIX PTYs and synthetic loopback API; no native VS Code rendering claims.',
                'limits': ['No native Windows or Linux execution implied by missing-pixel-metadata geometry probes.',
                           'No xterm renderer, GPU, reload/serialization, remote host or blocked-write proof.',
                           'Cancellation covers idle API and post-partial waiting, not interrupted PNG encoding.'],
                'results': results}
    if args.baseline:
        baseline = pathlib.Path(args.baseline).resolve()
        evidence['baseline'] = {'binary': str(baseline), 'sha256': hashlib.sha256(baseline.read_bytes()).hexdigest(),
                                'observation': 'Same OPENAI_VSCODE_IMAGES=1 request emitted color blocks on baseline; candidate emitted complete IIP PNG.'}
    result_path.write_text(json.dumps(evidence, indent=2) + '\n')
    print(f'{len(results)} checks passed; {result_path}')


if __name__ == '__main__':
    main()
