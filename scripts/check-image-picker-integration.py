#!/usr/bin/env python3
"""Check the integrated images generate trigger with PTYs and a loopback API.

Run with python3 -I -B scripts/check-image-picker-integration.py dist/openai OUTPUT.
Uses synthetic prompts and credentials; never contacts a production API.
"""
import argparse
import importlib.util
import json
import os
import pathlib
import pty
import shutil
import signal
import subprocess
import tempfile
import threading
import time


spec = importlib.util.spec_from_file_location(
    'picker_harness', pathlib.Path(__file__).with_name('image_picker_harness.py'))
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)


class Fixture(picker.Fixture):
    def do_POST(self):
        self.server.request_headers.append({key.lower(): value for key, value in self.headers.items()})
        self.server.request_paths.append(self.path)
        super().do_POST()


def check_body(actual, expected):
    for key, value in expected.items():
        assert key in actual and actual[key] == value, (key, expected, actual)


def check_saved(home, directory=None, filename=None):
    directory = directory or home/'Downloads'/'gpt-images'
    files = list(directory.glob('*.png'))
    assert len(files) == 1 and files[0].read_bytes() == picker.PNG, files
    if filename is not None:
        assert files[0].name == filename, files


def wait_resumed(terminal, mark):
    terminal.wait('Saved image:', after=mark)
    saved = terminal.raw.index(b'Saved image:', mark)
    terminal.wait('Images', after=saved)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    with tempfile.TemporaryDirectory(prefix='image-picker-integration-') as temporary:
        root = pathlib.Path(temporary)
        server = picker.http.server.ThreadingHTTPServer(('127.0.0.1', 0), Fixture)
        server.requests, server.request_headers, server.request_paths = [], [], []
        server.mode = 'ok'
        server.request_started, server.release = threading.Event(), threading.Event()
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base_url = f'http://127.0.0.1:{server.server_port}/v1'
        base_env = {'PATH': '/usr/bin:/bin', 'TERM': 'xterm-256color', 'LANG': 'en_US.UTF-8',
                    'OPENAI_API_KEY': 'synthetic-picker-key', 'OPENAI_BASE_URL': base_url, 'CI': 'true'}

        def environment(name, extra=None):
            home = root/name
            home.mkdir()
            return home, dict(base_env, HOME=str(home), **(extra or {}))

        def passed(name):
            results.append({'case': name, 'result': 'pass'})
            print('PASS', name, flush=True)

        def direct_tty(name, arguments, expected_status=1, expected_body=None, extra_env=None,
                       input_file=None, output_file=None, save_directory=None, save_name=None,
                       expected_error='Missing required options: --prompt'):
            home, env = environment(name, extra_env)
            before = len(server.requests)
            terminal = picker.Terminal(binary, arguments, env, input_file=input_file, output_file=output_file)
            try:
                terminal.finish(expected_status, expect_picker=False)
                if expected_status == 1:
                    assert expected_error in terminal.text(), terminal.text()
                assert len(server.requests)-before == (1 if expected_body is not None else 0), name
                if expected_body is not None:
                    check_body(server.requests[-1], expected_body)
                    check_saved(home, save_directory, save_name)
                assert not (home/'Library'/'Application Support'/'openai'/'image-picker.json').exists()
                assert not (home/'.config'/'openai'/'image-picker.json').exists()
                passed(name)
            finally:
                try:
                    terminal.save(output/name)
                finally:
                    terminal.close()

        def direct_pipe(name, arguments, data=b'', expected_status=1, expected_body=None):
            home, env = environment(name)
            before = len(server.requests)
            result = subprocess.run([binary, *arguments], input=data, capture_output=True, env=env, timeout=12)
            (output/(name+'.stdout')).write_bytes(result.stdout)
            (output/(name+'.stderr')).write_bytes(result.stderr)
            assert result.returncode == expected_status, (name, result.returncode, result.stderr)
            if expected_status == 1:
                assert b'Missing required options: --prompt' in result.stderr, result.stderr
            assert b'\x1b' not in result.stdout+result.stderr, (name, 'terminal sequences in redirected output')
            assert len(server.requests)-before == (1 if expected_body is not None else 0), name
            if expected_body is not None:
                check_body(server.requests[-1], expected_body)
                if '--format' in arguments:
                    assert json.loads(result.stdout)['data'][0]['b64_json'], result.stdout
                else:
                    check_saved(home)
            passed(name)

        try:
            name = 'bare-tty-early-paste'
            home, env = environment(name)
            before = len(server.requests)
            terminal = picker.Terminal(binary, ['images', 'generate'], env)
            prompt = "First line\nSecond 'quoted' line"
            try:
                deadline = time.monotonic()+5
                while b'\x1b[?2004h' not in terminal.raw:
                    terminal.read()
                    assert terminal.child.poll() is None and time.monotonic() < deadline, 'paste mode not enabled'
                assert 'Images' not in terminal.text(), 'early-paste probe missed the startup window'
                terminal.send(b'\x1b[200~'+prompt.encode()+b'\x1b[201~\r\x07\x10')
                picker.ready(terminal)
                terminal.wait('Images')
                assert len(server.requests) == before, 'startup input submitted an unseen request'
                terminal.send(picker.PRINT)
                terminal.finish(0)
                printed = picker.ANSI.sub('', terminal.raw.rsplit(b'\x1b[?2004l', 1)[-1].decode()).strip()
                assert printed.count('openai images generate ') == 1, printed
                # Multiline prompts use ANSI-C shell quoting, which Python's
                # shlex does not implement. Parse with the real target shell;
                # this local function records arguments instead of calling CLI.
                shell = shutil.which('zsh') or shutil.which('bash')
                assert shell, 'zsh or bash is required to check printed prompt quoting'
                parsed = subprocess.run([shell, '-f'],
                    input="openai() { printf '%s\\0' \"$@\"; }\n"+printed+'\n',
                    capture_output=True, text=True, env=env, timeout=5)
                assert parsed.returncode == 0, parsed.stderr
                arguments = parsed.stdout.split('\0')[:-1]
                assert arguments[:2] == ['images', 'generate'], arguments
                assert dict(zip(arguments[2::2], arguments[3::2]))['--prompt'] == prompt
                assert len(server.requests) == before
                assert not list(home.glob('Downloads/gpt-images/*'))
                assert not (home/'Library'/'Application Support'/'openai'/'image-picker.json').exists()
                assert not (home/'.config'/'openai'/'image-picker.json').exists()
                passed(name)
            finally:
                try:
                    terminal.save(output/name)
                finally:
                    terminal.close()

            for action in ['cancel', 'print', 'generate', 'repeated-enter', 'root-connection-options',
                           'changed-model', 'literal-at-prompt', 'api-error', 'cancel-request', 'two-generations']:
                name = 'bare-tty-'+action
                home, env = environment(name)
                arguments = ['images', 'generate']
                if action == 'root-connection-options':
                    arguments = ['--api-key', 'synthetic-root-override-key', '--base-url', base_url+'/override',
                                 '--organization', 'org_synthetic_picker', '--project', 'proj_synthetic_picker',
                                 *arguments]
                before = len(server.requests)
                server.mode = 'error' if action == 'api-error' else 'slow' if action == 'cancel-request' else 'ok'
                server.request_started.clear()
                server.release.clear()
                terminal = picker.Terminal(binary, arguments, env)
                prompt = "Synthetic integrated 'blue cat'"
                if action == 'literal-at-prompt':
                    prompt = "@not-a-local-file 'quoted'\n$(touch NOT_RUN)"
                try:
                    picker.ready(terminal)
                    terminal.wait('Images')
                    if action == 'cancel':
                        terminal.send(b'\x03')
                        terminal.finish(130)
                        restored = terminal.raw.rsplit(b'\x1b[?2004l', 1)[-1].decode('utf-8', 'replace')
                        assert not picker.ANSI.sub('', restored).strip(), 'cancel printed a spurious command error'
                    else:
                        terminal.send(b'\x1b[200~'+prompt.encode()+b'\x1b[201~')
                        if action == 'changed-model':
                            terminal.send(picker.DOWN+b'\r'+picker.DOWN+b'\r')
                        if action == 'print':
                            terminal.send(picker.PRINT)
                            terminal.finish(0)
                            assert picker.printed_flags(terminal)['--prompt'] == prompt
                        else:
                            mark = len(terminal.raw)
                            if action == 'changed-model':
                                terminal.send(b'\x07')  # Ctrl+G submits from the options region.
                            else:
                                terminal.send(b'\r'*(12 if action == 'repeated-enter' else 1))
                            picker.wait_for_request(terminal, server.request_started)
                            if action == 'cancel-request':
                                terminal.wait('Generating image')
                                os.kill(terminal.child.pid, signal.SIGINT)
                                terminal.finish(130)
                            elif action == 'api-error':
                                terminal.finish(1)
                                assert '401 Unauthorized' in terminal.text(), terminal.text()
                                assert 'Authentication failed.' in terminal.text(), terminal.text()
                            else:
                                wait_resumed(terminal, mark)
                                if action == 'two-generations':
                                    # Wait for the visible resumed prompt to accept a new submit.
                                    until = time.monotonic()+1.0
                                    while time.monotonic() < until:
                                        terminal.read(0.02)
                                    server.request_started.clear()
                                    second = len(terminal.raw)
                                    terminal.send(b' appended\r')
                                    picker.wait_for_request(terminal, server.request_started)
                                    wait_resumed(terminal, second)
                                    assert server.requests[-1]['prompt'] == prompt+' appended'
                                    prompt += ' appended'
                                terminal.send(b'\x03')
                                terminal.finish(130)
                            model = 'gpt-image-2.5-flare' if action == 'changed-model' else 'gpt-image-2.5-sunburst'
                            check_body(server.requests[-1], {'prompt': prompt, 'model': model,
                                       'size': '1024x1024', 'quality': 'auto', 'output_format': 'png',
                                       'background': 'auto', 'n': 1})
                            if action in {'api-error', 'cancel-request'}:
                                assert not list(home.glob('Downloads/gpt-images/*')), name
                            else:
                                if action == 'two-generations':
                                    saved = list((home/'Downloads'/'gpt-images').glob('*.png'))
                                    assert len(saved) == 2 and all(f.read_bytes() == picker.PNG for f in saved)
                                else:
                                    check_saved(home)
                    assert not list(home.rglob('image-picker.json')), 'picker state was persisted'
                    generates = action not in {'cancel', 'print'}
                    assert len(server.requests)-before == int(generates)+int(action == 'two-generations'), name
                    if action == 'root-connection-options':
                        headers = server.request_headers[-1]
                        assert headers['authorization'] == 'Bearer synthetic-root-override-key', headers
                        assert headers['openai-organization'] == 'org_synthetic_picker', headers
                        assert headers['openai-project'] == 'proj_synthetic_picker', headers
                        assert server.request_paths[-1] == '/v1/override/images/generations', server.request_paths[-1]
                    passed(name)
                finally:
                    server.release.set()
                    try:
                        terminal.save(output/name)
                    finally:
                        terminal.close()

            server.mode = 'ok'
            output_master, output_slave = pty.openpty()
            try:
                read_only_output = os.open(os.ttyname(output_slave), os.O_RDONLY | os.O_NOCTTY)
                try:
                    direct_tty('read-only-tty-output', ['images', 'generate'], output_file=read_only_output,
                               expected_error='Could not display the image picker. No new image request was started.')
                finally:
                    os.close(read_only_output)
            finally:
                os.close(output_master)
                os.close(output_slave)

            direct_tty('help-tty', ['images', 'generate', '--help'], expected_status=0)
            direct_tty('full-help-tty', ['help', '--all', 'images', 'generate'], expected_status=0)
            direct_tty('dumb-terminal', ['images', 'generate'], extra_env={'TERM': 'dumb'})
            direct_tty('positional-argument', ['images', 'generate', 'unexpected'])
            for flag, value in [('model', 'future-image-model'), ('size', '1536x864'), ('quality', 'high'),
                                ('count', '2'), ('output-format', 'webp'), ('background', 'transparent'),
                                ('name', 'synthetic-name'), ('inline', 'off'), ('moderation', 'low')]:
                direct_tty('local-'+flag+'-without-prompt', ['images', 'generate', '--'+flag, value])
            direct_tty('local-stream-false-without-prompt', ['images', 'generate', '--stream', 'false'])
            direct_tty('local-count-zero-without-prompt', ['images', 'generate', '--count', '0'])
            for flag, value in [('format', 'json'), ('format', 'text'), ('format-error', 'json'),
                                ('transform', 'data'), ('transform-error', 'message'),
                                ('raw-output', 'true'), ('raw-output', 'false')]:
                direct_tty('root-'+flag+'-'+value, ['--'+flag+'='+value, 'images', 'generate'])

            direct_tty('explicit-prompt-tty', ['images', 'generate', '--prompt', "A direct 'quoted' robot"],
                       expected_status=0, expected_body={'prompt': "A direct 'quoted' robot"})
            direct_tty('explicit-empty-prompt-tty', ['images', 'generate', '--prompt', ''],
                       expected_status=0, expected_body={'prompt': ''})
            target = root/'explicit-save-target'
            target.mkdir()
            direct_tty('explicit-options-preserved', ['images', 'generate', '--prompt', 'Direct settings',
                       '--model', 'gpt-image-2.5-flare', '--size', '1536x864', '--quality', 'high',
                       '--background', 'transparent', '--output-format', 'webp', '--count', '2',
                       '--moderation', 'low', '--output-compression', '67', '--stream', 'false',
                       '--output-dir', str(target), '--name', 'preserved', '--inline', 'off'],
                       expected_status=0, expected_body={'prompt': 'Direct settings', 'model': 'gpt-image-2.5-flare',
                       'size': '1536x864', 'quality': 'high', 'background': 'transparent', 'output_format': 'webp',
                       'n': 2, 'moderation': 'low', 'output_compression': 67, 'stream': False},
                       save_directory=target, save_name='preserved.png')
            direct_tty('output-dir-without-prompt', ['images', 'generate', '--output-dir', str(target)])

            direct_pipe('help-pipe', ['images', 'generate', '--help'], expected_status=0)
            direct_pipe('missing-prompt-pipe', ['images', 'generate'])
            direct_pipe('direct-prompt-pipe', ['images', 'generate', '--prompt', 'Pipe direct'],
                        expected_status=0, expected_body={'prompt': 'Pipe direct'})
            payload = {'prompt': "Piped 'JSON' prompt", 'model': 'gpt-image-2.5-flare', 'size': '1536x864',
                       'quality': 'high', 'background': 'transparent', 'output_format': 'png', 'n': 2}
            direct_pipe('stdin-json', ['images', 'generate'], json.dumps(payload).encode(),
                        expected_status=0, expected_body=payload)
            direct_pipe('stdin-json-explicit-prompt-wins', ['images', 'generate', '--prompt', 'Flag wins'],
                        json.dumps(payload).encode(), expected_status=0, expected_body=dict(payload, prompt='Flag wins'))
            direct_pipe('structured-json-direct', ['--format', 'json', 'images', 'generate', '--prompt', 'Structured'],
                        expected_status=0, expected_body={'prompt': 'Structured'})
            with tempfile.TemporaryFile() as source:
                source.write(json.dumps(payload).encode())
                source.seek(0)
                direct_tty('stdin-json-stdout-tty', ['images', 'generate'], expected_status=0,
                           expected_body=payload, input_file=source)
            with (output/'tty-stdin-stdout-file.stdout').open('wb') as destination:
                direct_tty('tty-stdin-stdout-file', ['images', 'generate'], output_file=destination)
            assert b'\x1b' not in (output/'tty-stdin-stdout-file.stdout').read_bytes()
        finally:
            server.release.set()
            server.shutdown()
            server.server_close()
    (output/'results.json').write_text(json.dumps(results, indent=2)+'\n')


if __name__ == '__main__':
    main()
