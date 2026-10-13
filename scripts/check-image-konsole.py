#!/usr/bin/env python3
"""Check the Konsole assertion through a built CLI with synthetic responses.

    python3 -I -B scripts/check-image-konsole.py OPENAI_BINARY OUTPUT_DIR [--baseline BASELINE_BINARY]

OPENAI_KONSOLE_IMAGES=1 asserts a Konsole build with KDE MR 1339 applied.
KONSOLE_VERSION supplies a protocol floor, not proof of that patch.
POSIX PTYs check emitted bytes and process contracts, not native Konsole rendering.
The PTYs have zero pixel dimensions, so geometry uses the existing cell estimate.
"""
import argparse
import base64
import hashlib
import http.server
import importlib.util
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
import termios
import threading
import time
import zlib

SCRIPTS = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
from image_picker_harness import Terminal, wait_for_request
from terminal_test import kitty_images

# Reuse only pure PNG helpers and the synthetic HTTP fixture. Importing this
# sibling checker does not run its main function or its VS Code assertions.
spec = importlib.util.spec_from_file_location('image_fixtures', SCRIPTS / 'check-image-vscode.py')
fixtures = importlib.util.module_from_spec(spec)
spec.loader.exec_module(fixtures)
require, Response = fixtures.require, fixtures.Response
IIP = re.compile(rb'\x1b\]1337;File=([^:\x07]+):([A-Za-z0-9+/=]*)(?:\x07|\x1b\\)')
KITTY = re.compile(rb'\x1b_G([^;]*);([A-Za-z0-9+/=]*)\x1b\\')


def large_png():
    """An incompressible fixture keeps output active after its first PTY read."""
    pixels = hashlib.shake_256(b'konsole-blocked-output').digest(1024 * 1024 * 3)

    def chunk(kind, data):
        return struct.pack('>I', len(data)) + kind + data + struct.pack('>I', zlib.crc32(kind + data))

    rows = b''.join(b'\0' + pixels[y * 3072:(y + 1) * 3072] for y in range(1024))
    return (b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('>IIBBBBB', 1024, 1024, 8, 2, 0, 0, 0))
            + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b''))


def check_frames(raw, widths, shape, protocol='iterm'):
    frames = IIP.findall(raw)
    require(raw.count(b'\x1b]1337;File=') == len(frames), 'incomplete IIP frame')
    if protocol == 'kitty':
        chunks = KITTY.findall(raw)
        images = kitty_images(raw)
        require(not frames and len(images) == 1, 'stronger identity lost its Kitty protocol')
        fields = dict(item.split(b'=', 1) for item in chunks[0][0].split(b','))
        require(fields.get(b'c') == str(widths[0]).encode(), 'wrong Kitty cell width')
        require(fixtures.decode_png(images[0]) == shape, 'Kitty pixels changed')
    else:
        require(b'\x1b_G' not in raw, 'unexpected Kitty output')
        require(len(frames) == len(widths), f'expected {len(widths)} frames, got {len(frames)}')
        for (header, payload), width in zip(frames, widths):
            fields = dict(item.split(b'=', 1) for item in header.split(b';'))
            image = base64.b64decode(payload, validate=True)
            require(fields.get(b'width') == str(width).encode() and fields.get(b'height') == b'auto',
                    f'wrong IIP geometry: {fields}')
            require(fields.get(b'inline') == b'1' and fields.get(b'size') == str(len(image)).encode(),
                    'wrong IIP inline flag or size')
            require(fixtures.decode_png(image) == shape, 'IIP dimensions, RGB pixels, or alpha changed')
    require(b'1337;Clear' not in raw, 'preview cleared earlier images')
    require(all(query not in raw for query in (b'\x1b[6n', b'\x1b[c', b'\x1b[0c', b'\x1b]11;?')),
            'direct command queried the terminal')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    parser.add_argument('--baseline')
    args = parser.parse_args()
    binary, output = pathlib.Path(args.binary).resolve(), pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    result_path = output / 'results.json'
    result_path.unlink(missing_ok=True)
    results = []
    evidence = {'binary': str(binary), 'binary_sha256': hashlib.sha256(binary.read_bytes()).hexdigest(),
                'checker_sha256': hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest(),
                'helper_sha256': {name: hashlib.sha256((SCRIPTS / name).read_bytes()).hexdigest()
                                  for name in ('check-image-vscode.py', 'image_picker_harness.py', 'terminal_test.py')},
                'host': {'system': platform.system(), 'machine': platform.machine(), 'python': platform.python_version()},
                'scope': 'Built CLI, sized POSIX PTYs, synthetic loopback API, no native Konsole rendering claim.',
                'limits': ['No native terminal resize, scrollback, or patch-detection proof.',
                           'Blocked-write cancellation suspends output on a private, non-controlling PTY before SIGINT.'],
                'results': results}
    normal, pixels = fixtures.fixture_png(8, 4)
    shape = (8, 4, pixels)
    with tempfile.TemporaryDirectory(prefix='openai-konsole-process-') as temporary:
        root = pathlib.Path(temporary)
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), fixtures.Fixture)
        server.case = Response('ok', normal)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base_env = {'PATH': '/usr/bin:/bin', 'LANG': 'C.UTF-8', 'TERM': 'xterm-256color', 'TERM_PROGRAM': '',
                    'KONSOLE_VERSION': '261170', 'OPENAI_KONSOLE_IMAGES': '1', 'OPENAI_API_KEY': 'synthetic-key',
                    'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1'}

        def environment(name, extra=None):
            home = root / name
            home.mkdir(exist_ok=True)
            env = dict(base_env, HOME=str(home), XDG_CONFIG_HOME=str(home / 'config'), APPDATA=str(home / 'config'))
            env.update(extra or {})
            return home, env

        def saved_files(home, count, image):
            saved = list((home / 'Downloads' / 'gpt-images').glob('*.png'))
            require(len(saved) == count and all(path.read_bytes() == image for path in saved),
                    f'saved originals changed: expected {count}, got {len(saved)}')

        def passed(name, **details):
            results.append(dict(case=name, result='pass', **details))
            print('PASS', name, flush=True)

        def tty(name, arguments, *, extra=None, widths=(64,), protocol='iterm', case=None,
                expected=0, saved=1, interact=None, home_name=None, geometry=(80, 24),
                image_shape=shape, executable=binary, stderr_tty=False, input_data=None, expected_stderr=b''):
            home, env = environment(home_name or name, extra)
            server.case = case = case or Response('ok', normal)
            stderr_path = output / (name + '.stderr')
            with stderr_path.open('wb') as stderr:
                terminal = Terminal(str(executable), arguments, env, width=geometry[0], height=geometry[1],
                                    input_file=subprocess.PIPE if input_data is not None else None,
                                    stderr_file=None if stderr_tty else stderr)
            try:
                if input_data is not None:
                    terminal.child.stdin.write(input_data)
                    terminal.child.stdin.close()
                if interact:
                    interact(terminal, case)
                terminal.finish(expected, expect_picker=False)
                raw, error = bytes(terminal.raw), stderr_path.read_bytes()
                check_frames(raw, widths, image_shape, protocol)
                require(not case.errors, case.errors)
                require(len(case.requests) == int('generate' in arguments), 'wrong API request count')
                if '--partial-images' in arguments:
                    require(case.requests[0].get('stream') is True and case.requests[0].get('partial_images') == 2,
                            'progress request changed')
                if expected == 0:
                    require(error == expected_stderr, f'stderr changed: {error!r}')
                elif not stderr_tty:
                    require(error and b'\x1b]1337;' not in error, 'missing or contaminated error diagnostic')
                if expected == 130:
                    require(b'cancel' in raw.lower(), 'missing cancellation diagnostic')
                    require(time.monotonic() - terminal.started < 5, 'cancellation was not prompt')
                saved_files(home, saved, case.image)
                passed(name, exit=expected, frames=len(widths), widths=list(widths), protocol=protocol,
                       saved=saved, stdin='pipe' if input_data is not None else 'pty',
                       stderr='pty' if stderr_tty else 'separate',
                       seconds=round(time.monotonic() - terminal.started, 3))
                return raw, error
            except Exception as exc:
                results.append(dict(case=name, result='fail', error=str(exc)))
                raise
            finally:
                case.release.set()
                try:
                    terminal.save(output / name)
                finally:
                    terminal.close()

        def blocked_preview(name, extra=None):
            home, env = environment(name, extra)
            source = home / 'synthetic large image.png'
            image = large_png()
            source.write_bytes(image)
            stderr_path = output / (name + '.stderr')
            # A controlling session can delay Darwin's exit while unread data
            # remains. Test the writer independently of that kernel teardown.
            with stderr_path.open('wb') as stderr:
                terminal = Terminal(str(binary), ['images', 'preview', str(source)], env, stderr_file=stderr)
            suspended = False
            try:
                deadline = time.monotonic() + 12
                while len(terminal.raw) < 4096 or b'\x1b]1337;File=' not in terminal.raw:
                    require(terminal.child.poll() is None and time.monotonic() < deadline,
                            'large preview did not begin its image payload')
                    terminal.read()
                # Suspend this owned terminal's output after the payload starts.
                # No reader or kernel queue timing can release the writer before
                # cancellation. Restore output only after the child has exited.
                termios.tcflow(terminal.slave, termios.TCOOFF)
                suspended = True
                started = time.monotonic()
                terminal.child.send_signal(signal.SIGINT)
                try:
                    status = terminal.child.wait(timeout=4)
                except subprocess.TimeoutExpired:
                    raise AssertionError('image output stayed blocked after SIGINT') from None
                elapsed = time.monotonic() - started
                require(status == 1, f'local preview cancellation changed exit status: {status}')
                require(b'cancel' in stderr_path.read_bytes().lower(), 'missing cancellation diagnostic')
                require(source.read_bytes() == image, 'blocked preview changed the original')
                termios.tcflow(terminal.slave, termios.TCOON)
                suspended = False
                terminal.finish(1, expect_picker=False)
                passed(name, exit=status, cancellation_seconds=round(elapsed, 3),
                       output_suspended_until_exit=True, reader_stopped_until_exit=True, original_preserved=True)
            except Exception as exc:
                results.append(dict(case=name, result='fail', error=str(exc)))
                raise
            finally:
                # Release only this owned PTY on a failed regression. Reap the
                # child even when an older binary cannot cancel its tty write.
                try:
                    if suspended:
                        termios.tcflow(terminal.slave, termios.TCOON)
                finally:
                    try:
                        if terminal.child.poll() is None:
                            terminal.child.kill()
                        terminal.child.wait(timeout=5)
                    finally:
                        try:
                            terminal.save(output / name)
                        finally:
                            terminal.close()

        def cancel_after_saved_receipt():
            name = 'cancel-after-saved-receipt'
            home, env = environment(name)
            image = large_png()
            server.case = case = Response('ok', image)
            terminal = Terminal(str(binary), generate, env)
            try:
                deadline = time.monotonic() + 12
                while b'\x1b]1337;File=' not in terminal.raw:
                    require(terminal.child.poll() is None and time.monotonic() < deadline,
                            'saved native preview did not start')
                    terminal.read()
                before = bytes(terminal.raw)
                receipt, frame = before.find(b'Saved image:'), before.index(b'\x1b]1337;File=')
                require(0 <= receipt < frame, 'native preview started before its saved receipt')
                require(b'\x07' not in before[frame:] and b'\x1b\\' not in before[frame:],
                        'preview completed before the cancellation gate')
                saved_files(home, 1, image)
                started = time.monotonic()
                terminal.child.send_signal(signal.SIGINT)
                terminal.finish(130, expect_picker=False)
                elapsed = time.monotonic() - started
                require(elapsed < 5, 'saved-image cancellation was not prompt')
                raw = bytes(terminal.raw)
                message = (b'Images were saved, but the command was canceled. '
                           b'Use the saved files; no need to generate again.')
                recovery = raw.find(message)
                terminator = raw.find(b'\x1b\\', frame)
                require(recovery >= 0, 'canceled generation lost saved-image recovery guidance')
                require(frame < terminator < recovery, 'saved-image recovery did not follow a terminated frame')
                saved_files(home, 1, image)
                require(len(case.requests) == 1 and not case.errors, 'cancellation changed the API request lifecycle')
                passed(name, exit=130, saved=1, cancellation_seconds=round(elapsed, 3),
                       original_preserved=True, recovery_after_terminated_frame=True)
            except Exception as exc:
                results.append(dict(case=name, result='fail', error=str(exc)))
                raise
            finally:
                case.release.set()
                try:
                    terminal.save(output / name)
                finally:
                    terminal.close()

        generate = ['images', 'generate', '--prompt', 'Synthetic Konsole fixture']
        stream = [*generate, '--partial-images', '2', '--max-items', '-1']
        try:
            if args.baseline:
                baseline = pathlib.Path(args.baseline).resolve()
                raw, _ = tty('baseline-assertion-unrecognized', generate, executable=baseline, widths=())
                require('▀'.encode() in raw, 'baseline lost its existing color-block fallback')
                evidence['baseline'] = {'binary': str(baseline), 'sha256': hashlib.sha256(baseline.read_bytes()).hexdigest(),
                                        'observation': 'Identical assertion emitted blocks before this change, then complete IIP PNG.'}
            tty('enabled-auto', generate)
            tty('enabled-on', generate + ['--inline', 'on'])
            tty('protocol-floor', generate, extra={'KONSOLE_VERSION': '220400'})
            tty('no-color-native', generate, extra={'NO_COLOR': '1'})
            tty('explicit-off', generate + ['--inline', 'off'], widths=())
            raw, _ = tty('remember-off', ['images', 'inline', 'off'], home_name='preferences', widths=(), saved=0,
                         expected_stderr=b'Automatic image previews off.\nOverride once: --inline auto|on|off\n'
                                         b'View a saved image: openai images preview FILE\n')
            require(not raw, 'preference confirmation contaminated stdout')
            tty('persisted-off', generate, home_name='preferences', widths=())
            tty('override-off-with-on', generate + ['--inline', 'on'], home_name='preferences', saved=2)
            for name, value in [('default', ''), ('disabled', '0'), ('invalid', 'true'), ('whitespace', '1 ')]:
                raw, _ = tty('assertion-' + name, generate, extra={'OPENAI_KONSOLE_IMAGES': value}, widths=())
                require('▀'.encode() in raw, 'missing assertion changed the color-block fallback')
            for version in ('', '220399', '26.11.70', '26117', '2611700', '26117x'):
                tty('version-' + (version or 'missing'), generate,
                    extra={'KONSOLE_VERSION': version, 'NO_COLOR': '1'}, widths=())
            for name, extra in [('ci', {'CI': 'true'}), ('dumb', {'TERM': 'dumb'}),
                                ('tmux', {'TMUX': 'synthetic'}), ('screen', {'STY': 'synthetic'}),
                                ('zellij', {'ZELLIJ': 'synthetic'}), ('term-tmux', {'TERM': 'tmux-256color'}),
                                ('term-screen', {'TERM': 'screen-256color'}),
                                ('unknown-program', {'TERM_PROGRAM': 'unknown'}),
                                ('vscode-without-assertion', {'TERM_PROGRAM': 'vscode'})]:
                tty(name, generate, extra=dict(extra, NO_COLOR='1'), widths=())
            for name, extra, protocol, width in [
                    ('vscode-wins', {'TERM_PROGRAM': 'vscode', 'OPENAI_VSCODE_IMAGES': '1'}, 'iterm', 'auto'),
                    ('iterm-wins', {'TERM_PROGRAM': 'iTerm.app', 'KONSOLE_VERSION': ''}, 'iterm', 64),
                    ('kitty-program-wins', {'TERM_PROGRAM': 'kitty'}, 'kitty', 64),
                    ('kitty-term-wins', {'TERM': 'xterm-kitty'}, 'kitty', 64),
                    ('ghostty-term-wins', {'TERM': 'xterm-ghostty'}, 'kitty', 64)]:
                tty(name, generate, extra=extra, protocol=protocol, widths=(width,))

            source = root / 'fixture with spaces.png'
            source.write_bytes(normal)
            tty('local-preview', ['images', 'preview', str(source)], saved=0)
            require(source.read_bytes() == normal, 'local preview changed its source')
            if args.baseline:
                arguments = ['images', 'preview', str(source)]
                baseline_raw, _ = tty('baseline-local-fallback', arguments, executable=baseline,
                                      extra={'OPENAI_KONSOLE_IMAGES': ''}, widths=(), saved=0)
                candidate_raw, _ = tty('candidate-local-fallback', arguments,
                                       extra={'OPENAI_KONSOLE_IMAGES': ''}, widths=(), saved=0)
                require(candidate_raw == baseline_raw and '▀'.encode() in candidate_raw,
                        'default Konsole fallback changed')
            portrait, portrait_pixels = fixtures.fixture_png(8, 32)
            source.write_bytes(portrait)
            tty('portrait-height-fit', ['images', 'preview', str(source)], saved=0, geometry=(18, 8),
                widths=(3,), image_shape=(8, 32, portrait_pixels))

            def release_resized(terminal, case):
                wait_for_request(terminal, case.started)
                terminal.resize(17, 7)
                case.release.set()

            tty('current-width-after-request', generate, case=Response('ok', normal, True),
                interact=release_resized, widths=(16,))

            executable = root / 'removed executable'
            shutil.copy2(binary, executable)

            def remove_executable(terminal, case):
                wait_for_request(terminal, case.started)
                executable.unlink()
                case.release.set()

            tty('prepared-before-executable-removal', generate, executable=executable,
                case=Response('ok', normal, True), interact=remove_executable)

            def preserve_input(terminal, case):
                wait_for_request(terminal, case.started)
                queued = b'echo synthetic-next-command\n'
                terminal.send(queued)
                case.release.set()
                terminal.finish(0, expect_picker=False)
                require(select.select([terminal.slave], [], [], 1)[0], 'preview consumed queued input')
                require(os.read(terminal.slave, 4096) == queued, 'preview changed queued input')

            tty('queued-input', generate, case=Response('ok', normal, True), interact=preserve_input)
            for stderr_tty in (False, True):
                name = 'piped-json-stdin-tty-stderr' if stderr_tty else 'piped-json-stdin'
                request = {'prompt': 'Synthetic piped Konsole input', 'n': 1, 'metadata': {'fixture': True}}
                case = Response('ok', normal)
                tty(name, ['images', 'generate'], case=case, stderr_tty=stderr_tty,
                    input_data=json.dumps(request).encode())
                require(case.requests[0]['prompt'] == request['prompt'] and
                        case.requests[0]['metadata'] == request['metadata'], 'piped JSON request changed')

            def partial(terminal, case, cancel=False):
                terminal.wait('Progress preview 1 of 2:')
                deadline = time.monotonic() + 8
                while not IIP.search(terminal.raw):
                    require(time.monotonic() < deadline, 'partial preview waited for final response')
                    terminal.read()
                check_frames(bytes(terminal.raw), (64,), shape)
                require(not case.release.is_set(), 'server already released final response')
                if cancel:
                    terminal.child.send_signal(signal.SIGINT)
                else:
                    terminal.resize(19, 9)
                    case.release.set()

            tty('progress-before-final-resized', stream, case=Response('stream-ok', normal, True),
                interact=partial, widths=(64, 18, 18))
            for mode in ('stream-error', 'stream-eof', 'stream-malformed'):
                tty(mode, stream, case=Response(mode, normal, True), interact=partial, expected=1, saved=0)

            def cancel_idle(terminal, case):
                wait_for_request(terminal, case.started)
                terminal.child.send_signal(signal.SIGINT)

            tty('cancel-idle', generate, case=Response('ok', normal, True), interact=cancel_idle,
                expected=130, saved=0, widths=(), stderr_tty=True)
            tty('cancel-partial', stream, case=Response('stream-ok', normal, True),
                interact=lambda terminal, case: partial(terminal, case, True), expected=130, saved=0, stderr_tty=True)
            tty('first-save', generate, home_name='repeat')
            tty('error-preserves-original', generate, home_name='repeat', case=Response('error', normal),
                expected=1, widths=())
            tty('retry-saves-original', generate, home_name='repeat', saved=2)
            raw, error = tty('partial-save-error', generate + ['--count', '2'],
                             case=Response('partial-save-error', normal), expected=1, widths=())
            require(b'Saved image:' in raw and b'Saved 1 image(s)' in error and b'listed files are kept' in error,
                    'partial save failure lost preserved-file guidance')
            for name, flags in [('json', ['--format', 'json']), ('raw', ['--raw-output']),
                                ('extract', ['--format', 'text', '--transform', 'data.0.b64_json'])]:
                raw, _ = tty(name, flags + generate, widths=(), saved=0)
                require(b'Saved image:' not in raw, 'explicit API mode unexpectedly saved an image')
                # Normalize only color and PTY line endings. Other controls or
                # prose must fail complete-document and exact-output checks.
                normalized = re.sub(rb'\x1b\[[0-9;]*m', b'', raw).replace(b'\r\n', b'\n').decode('utf-8')
                encoded = base64.b64encode(normal).decode('ascii')
                response = {'created': 1700000000, 'data': [{'b64_json': encoded}]}
                if name == 'json':
                    require(json.loads(normalized) == response, 'JSON response changed')
                elif name == 'raw':
                    require(normalized == json.dumps(response, indent=2) + '\n', 'raw API JSON output changed')
                else:
                    require(normalized == encoded + '\n', 'extracted API value changed')
            home, env = environment('stdout-pipe')
            server.case = Response('ok', normal)
            result = subprocess.run([str(binary), *generate], input=b'', capture_output=True, env=env, timeout=12)
            (output / 'stdout-pipe.stdout').write_bytes(result.stdout)
            (output / 'stdout-pipe.stderr').write_bytes(result.stderr)
            require(result.returncode == 0 and not result.stderr and b'Saved image:' in result.stdout,
                    'redirected save output changed')
            require(b'\x1b' not in result.stdout, 'redirected output emitted terminal controls')
            saved_files(home, 1, normal)
            require(not server.case.errors and len(server.case.requests) == 1, 'redirected API request changed')
            passed('stdout-pipe', exit=0, frames=0, saved=1)
            blocked_preview('cancel-blocked-vscode-control', {'TERM_PROGRAM': 'vscode', 'OPENAI_VSCODE_IMAGES': '1'})
            blocked_preview('cancel-blocked-preview')
            cancel_after_saved_receipt()
        except Exception as exc:
            evidence['failure'] = str(exc)
            raise
        finally:
            server.case.release.set()
            server.shutdown()
            server.server_close()
            thread.join(timeout=5)
            result_path.write_text(json.dumps(evidence, indent=2) + '\n')
    print(f'{len(results)} checks passed; {result_path}')


if __name__ == '__main__':
    main()
