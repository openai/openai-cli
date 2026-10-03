#!/usr/bin/env python3
"""Check native previews across picker requests and executable replacement.

Run with python3 -I -B scripts/check-image-picker-native.py BINARY OUTPUT.
Uses private PTYs, synthetic loopback responses and temporary homes. This checks
protocol pixels and terminal restoration, not graphical terminal appearance.
"""
import argparse
import base64
import importlib.util
import json
import pathlib
import re
import shutil
import struct
import tempfile
import threading
import time
import zlib


def load_script(name):
    spec = importlib.util.spec_from_file_location(
        name, pathlib.Path(__file__).with_name(name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


picker = load_script('image_picker_harness')
compatibility = load_script('check-image-output-compatibility')


def fixture_png():
    def chunk(kind, data):
        return (struct.pack('>I', len(data)) + kind + data
                + struct.pack('>I', zlib.crc32(kind + data) & 0xffffffff))
    return (b'\x89PNG\r\n\x1a\n'
            + chunk(b'IHDR', struct.pack('>IIBBBBB', 2, 1, 8, 6, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(b'\x00' + compatibility.PIXELS))
            + chunk(b'IEND', b''))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    args = parser.parse_args()
    binary = pathlib.Path(args.binary).resolve()
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    summary = output / 'results.json'
    summary.unlink(missing_ok=True)
    results = []
    png = fixture_png()
    cases = [('api', 'unchanged'), ('api', 'removed'), ('api', 'replaced'),
             ('picker', 'removed'), ('picker', 'replaced')]
    for phase, mode in cases:
        name = phase + '-' + mode
        with tempfile.TemporaryDirectory(prefix='image-picker-native-') as temporary:
            home = pathlib.Path(temporary)
            live = home / 'running cli'
            shutil.copyfile(binary, live)
            live.chmod(0o700)

            def replace_executable():
                live.rename(home / 'original cli')
                if mode == 'replaced':
                    live.write_text('#!/bin/sh\nexit 23\n')
                    live.chmod(0o700)

            class Fixture(picker.http.server.BaseHTTPRequestHandler):
                def log_message(self, *_):
                    pass

                def do_POST(self):
                    body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                    self.server.requests.append(body)
                    if phase == 'api' and len(self.server.requests) == 1 and mode != 'unchanged':
                        replace_executable()
                    data = json.dumps({'data': [{'b64_json': base64.b64encode(png).decode()}]}).encode()
                    self.send_response(200)
                    self.send_header('Content-Type', 'application/json')
                    self.send_header('Content-Length', str(len(data)))
                    self.end_headers()
                    self.wfile.write(data)

            server = picker.http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
            server.requests = []
            worker = threading.Thread(target=server.serve_forever, daemon=True)
            worker.start()
            env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'TERM': 'xterm-kitty',
                   'TERM_PROGRAM': 'kitty', 'LANG': 'en_US.UTF-8', 'CI': 'false',
                   'OPENAI_PICKER_SHELL': 'bash', 'OPENAI_API_KEY': 'synthetic-picker-key',
                   'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1'}
            terminal = None
            try:
                terminal = picker.Terminal(str(live), ['images', 'generate'], env)
                picker.ready(terminal)
                if phase == 'picker':
                    replace_executable()
                for number in range(2):
                    mark = len(terminal.raw)
                    terminal.send(b'\x1b[200~synthetic image ' + str(number).encode() + b'\x1b[201~\r')
                    terminal.wait('Saved image:', after=mark)
                    saved = terminal.raw.index(b'Saved image:', mark)
                    terminal.wait('Describe your image', after=saved)
                    # Respect the resumed picker's existing submit-key quiet guard.
                    until = time.monotonic() + 1
                    while time.monotonic() < until:
                        terminal.read(0.02)
                terminal.send(b'\x03')
                terminal.finish(130)
                assert len(server.requests) == 2, 'unexpected request count'
                files = list((home / 'Downloads' / 'gpt-images').glob('*.png'))
                assert len(files) == 2 and all(path.read_bytes() == png for path in files)
                frames = re.findall(rb'\x1b_G([^;]*);(.*?)\x1b\\', terminal.raw, re.S)
                assert len(frames) == terminal.raw.count(b'\x1b_G') == 2, 'missing or partial preview'
                for header, payload in frames:
                    fields = dict(field.split(b'=', 1) for field in header.split(b','))
                    assert all(fields.get(k) == v for k, v in
                               {b'a': b'T', b'f': b'100', b'm': b'0', b'q': b'2'}.items())
                    compatibility.check_fixture_png(base64.b64decode(payload, validate=True))
                results.append({'case': name, 'requests': 2, 'exact_pixel_previews': 2,
                                'saved_originals': 2, 'picker_returns': 2, 'exit': 130, 'passed': True})
                print('PASS', name, flush=True)
            finally:
                try:
                    if terminal is not None:
                        try:
                            terminal.save(output / name)
                        finally:
                            terminal.close()
                finally:
                    server.shutdown()
                    server.server_close()
                    worker.join()
    summary.write_text(json.dumps(results, indent=2) + '\n')


if __name__ == '__main__':
    main()
