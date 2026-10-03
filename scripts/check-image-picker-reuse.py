#!/usr/bin/env python3
"""Check remembered picker settings and save folders through the real CLI.

Run with python3 -I -B scripts/check-image-picker-reuse.py dist/openai OUTPUT.
Uses the shared PTY/loopback fixture, temporary homes, and synthetic inputs only.
"""
import argparse
import contextlib
import importlib.util
import json
import os
import pathlib
import subprocess
import sys
import tempfile
import threading


spec = importlib.util.spec_from_file_location(
    'picker_harness', pathlib.Path(__file__).with_name('image_picker_harness.py'))
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)


def paste(terminal, text):
    terminal.send(b'\x1b[200~'+text.encode()+b'\x1b[201~')


def open_folder_editor(terminal, from_prompt=True):
    mark = len(terminal.raw)
    if from_prompt:
        terminal.send(picker.DOWN)
    # Save to is immediately before More options in the compact settings list.
    terminal.send(picker.END+picker.UP+b'\r')
    terminal.wait('Current folder', after=mark)
    mark = len(terminal.raw)
    terminal.send(picker.HOME+picker.DOWN*2+b'\r')
    terminal.wait('Tab completes directory names', after=mark)
    terminal.send(b'\x15')


def select_folder(terminal, folder, from_prompt=True):
    open_folder_editor(terminal, from_prompt)
    paste(terminal, folder)
    mark = len(terminal.raw)
    terminal.send(b'\r')
    terminal.wait('Settings', after=mark)


def state_path(home, env):
    directory = home/'Library'/'Application Support' if sys.platform == 'darwin' else pathlib.Path(env['XDG_CONFIG_HOME'])
    return directory/'openai'/'image-picker.json'


def check_images(directory, expected):
    files = list(directory.glob('*.png'))
    assert len(files) == expected, (directory, files)
    assert all(path.read_bytes() == picker.PNG for path in files), 'saved bytes differ from fixture'


def check_body(body, prompt, model):
    assert body == {'prompt': prompt, 'model': model, 'size': '1024x1024',
                    'quality': 'auto', 'background': 'auto', 'output_format': 'png', 'n': 1}, body
    assert 'output_dir' not in body and 'output-dir' not in body, body


def wait_resumed(terminal, mark):
    terminal.wait('Saved image:', after=mark)
    saved = terminal.raw.index(b'Saved image:', mark)
    terminal.wait('Images', after=saved)
    terminal.wait('Describe your image', after=saved)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    args = parser.parse_args()
    binary = str(pathlib.Path(args.binary).resolve())
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    with tempfile.TemporaryDirectory(prefix='image-picker-reuse-') as temporary:
        root = pathlib.Path(temporary).resolve()
        server = picker.http.server.ThreadingHTTPServer(('127.0.0.1', 0), picker.Fixture)
        server.requests, server.mode = [], 'ok'
        server.request_started, server.release = threading.Event(), threading.Event()
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base_env = {'PATH': '/usr/bin:/bin', 'TERM': 'xterm-256color', 'LANG': 'en_US.UTF-8',
                    'OPENAI_API_KEY': 'synthetic-picker-key', 'CI': 'true',
                    'OPENAI_BASE_URL': f'http://127.0.0.1:{server.server_port}/v1'}

        def environment(name):
            home = root/name
            home.mkdir()
            env = dict(base_env, HOME=str(home), XDG_CONFIG_HOME=str(home/'config'),
                       APPDATA=str(home/'config'))
            return home, env

        def passed(name):
            results.append({'case': name, 'result': 'pass'})
            print('PASS', name, flush=True)

        @contextlib.contextmanager
        def terminal_case(name, env, arguments=None, cwd=None, interactive=True):
            server.request_started.clear()
            previous = pathlib.Path.cwd()
            try:
                if cwd is not None:
                    os.chdir(cwd)
                terminal = picker.Terminal(binary, arguments or ['images', 'generate'], env,
                                           width=140, height=28)
            finally:
                # The harness has no cwd option. Only process creation changes
                # cwd; cases run serially and the fixture never uses cwd.
                os.chdir(previous)
            try:
                if interactive:
                    picker.ready(terminal)
                    terminal.wait('Images')
                yield terminal
                passed(name)
            except BaseException:
                results.append({'case': name, 'result': 'fail'})
                raise
            finally:
                try:
                    terminal.save(output/name)
                finally:
                    terminal.close()

        try:
            home, env = environment('remembered')
            first_cwd, next_cwd = root/'first cwd', root/'different cwd'
            first_cwd.mkdir()
            next_cwd.mkdir()
            folder = first_cwd/'pictures with spaces 雪'
            folder.mkdir()
            prompt, model = "Synthetic reuse 'moon' 雪", 'gpt-image-2.5-flare'
            path = state_path(home, env)
            before = len(server.requests)
            with terminal_case('remember-custom-generation', env, cwd=first_cwd) as terminal:
                paste(terminal, prompt)
                terminal.send(picker.DOWN+b'\r')
                terminal.wait('Choose model')
                mark = len(terminal.raw)
                terminal.send(picker.DOWN+b'\r')
                terminal.wait('Settings', after=mark)
                select_folder(terminal, folder.name, from_prompt=False)
                mark = len(terminal.raw)
                terminal.send(b'\x07')
                picker.wait_for_request(terminal, server.request_started)
                wait_resumed(terminal, mark)
                # Capture submission output before the resumed picker's own
                # teardown becomes the final bracketed-paste marker.
                flags = picker.printed_flags(terminal)
                terminal.send(b'\x03')
                terminal.finish(130)
                assert len(server.requests) == before+1
                check_body(server.requests[-1], prompt, model)
                check_images(folder, 1)
                assert flags['--output-dir'] == str(folder), flags
                saved = json.loads(path.read_bytes())
                assert 'prompt' not in saved and saved['model'] == model, saved
                assert saved['output_dir'] == str(folder), saved
                assert not (home/'Downloads'/'gpt-images').exists()

            previous_state = path.read_bytes()
            before = len(server.requests)
            with terminal_case('restore-print-changed-cwd', env, cwd=next_cwd) as terminal:
                terminal.wait('Describe your image')
                terminal.wait(model)
                assert prompt not in terminal.text()
                mark = len(terminal.raw)
                terminal.send(b'\r')
                terminal.wait('Add a prompt first.', after=mark)
                assert len(server.requests) == before
                assert path.read_bytes() == previous_state
                print_prompt = 'An entirely new synthetic print prompt'
                paste(terminal, print_prompt)
                terminal.send(picker.PRINT)
                terminal.finish(0)
                restored_flags = picker.printed_flags(terminal)
                assert restored_flags == dict(flags, **{'--prompt': print_prompt}), (flags, restored_flags)
                assert path.read_bytes() == previous_state
                assert len(server.requests) == before
                assert not list(next_cwd.iterdir())

            with terminal_case('restore-generation-changed-cwd', env, cwd=next_cwd) as terminal:
                terminal.wait('Describe your image')
                assert prompt not in terminal.text() and print_prompt not in terminal.text()
                next_prompt = 'Another synthetic image with the remembered settings'
                paste(terminal, next_prompt)
                mark = len(terminal.raw)
                terminal.send(b'\r')
                picker.wait_for_request(terminal, server.request_started)
                wait_resumed(terminal, mark)
                terminal.send(b'\x03')
                terminal.finish(130)
                assert len(server.requests) == before+1
                check_body(server.requests[-1], next_prompt, model)
                check_images(folder, 2)
                assert not list(next_cwd.iterdir())
                assert path.read_bytes() == previous_state

            before = len(server.requests)
            with terminal_case('cancel-keeps-previous-state', env) as terminal:
                paste(terminal, 'Changed synthetic draft discarded on cancel')
                terminal.wait('Changed synthetic draft')
                terminal.send(picker.DOWN+b'\r')
                terminal.wait('Choose model')
                mark = len(terminal.raw)
                terminal.send(picker.HOME+b'\r')
                terminal.wait('Settings', after=mark)
                terminal.send(b'\x03')
                terminal.finish(130)
                assert path.read_bytes() == previous_state
                assert len(server.requests) == before

            with terminal_case('invalid-folder-keeps-previous-state', env) as terminal:
                missing = root/'folder that does not exist 雪'
                open_folder_editor(terminal)
                paste(terminal, str(missing))
                mark = len(terminal.raw)
                terminal.send(b'\r')
                terminal.wait('Choose an existing folder', after=mark)
                assert path.read_bytes() == previous_state
                assert not missing.exists()
                assert len(server.requests) == before
                terminal.send(b'\x03')
                terminal.finish(130)
                assert path.read_bytes() == previous_state

            legacy_home, legacy_env = environment('legacy-state')
            legacy_path = state_path(legacy_home, legacy_env)
            legacy_path.parent.mkdir(parents=True, mode=0o700)
            legacy_prompt = 'SYNTHETIC_LEGACY_PROMPT_MUST_NOT_RESTORE'
            legacy = dict(saved, version=1, prompt=legacy_prompt)
            legacy_bytes = (json.dumps(legacy)+'\n').encode()
            legacy_path.write_bytes(legacy_bytes)
            legacy_path.chmod(0o600)
            with terminal_case('legacy-state-blank-and-cancel', legacy_env) as terminal:
                terminal.wait('Describe your image')
                terminal.wait(model)
                assert legacy_prompt not in terminal.text()
                assert 'Could not restore saved settings' not in terminal.text()
                mark = len(terminal.raw)
                terminal.send(b'\r')
                terminal.wait('Add a prompt first.', after=mark)
                assert legacy_path.read_bytes() == legacy_bytes
                assert len(server.requests) == before
                paste(terminal, 'Legacy synthetic draft discarded on cancel')
                terminal.send(b'\x03')
                terminal.finish(130)
                assert legacy_path.read_bytes() == legacy_bytes
                assert len(server.requests) == before
                assert legacy_prompt not in terminal.text()

            with terminal_case('legacy-state-submit-removes-prompt', legacy_env) as terminal:
                terminal.wait('Describe your image')
                legacy_replacement = 'A fresh synthetic prompt with legacy settings'
                paste(terminal, legacy_replacement)
                terminal.wait(legacy_replacement)
                assert legacy_path.read_bytes() == legacy_bytes
                terminal.send(picker.PRINT)
                terminal.finish(0)
                assert picker.printed_flags(terminal) == dict(flags, **{'--prompt': legacy_replacement})
                assert json.loads(legacy_path.read_bytes()) == saved
                assert legacy_prompt not in terminal.text()
                assert 'Could not restore saved settings' not in terminal.text()
                assert 'Could not remember these settings' not in terminal.text()
                assert len(server.requests) == before

            completion_home, completion_env = environment('completion')
            completed = root/'completion parent'/'snow 雪 folder'
            completed.mkdir(parents=True)
            with terminal_case('complete-folder-spaces-unicode', completion_env) as terminal:
                paste(terminal, 'Synthetic completion prompt')
                open_folder_editor(terminal)
                paste(terminal, str(completed.parent/'snow'))
                mark = len(terminal.raw)
                terminal.send(b'\t')
                terminal.wait('snow 雪 folder', after=mark)
                mark = len(terminal.raw)
                terminal.send(b'\r')
                terminal.wait('Settings', after=mark)
                terminal.send(picker.PRINT)
                terminal.finish(0)
                assert picker.printed_flags(terminal)['--output-dir'] == str(completed)
                assert json.loads(state_path(completion_home, completion_env).read_bytes())['output_dir'] == str(completed)
                assert len(server.requests) == before
                assert not list(completed.iterdir())
                assert not (completion_home/'Downloads'/'gpt-images').exists()

            default_home, default_env = environment('default-print')
            with terminal_case('default-print-creates-no-image-folder', default_env) as terminal:
                paste(terminal, 'Synthetic print without generation')
                terminal.send(picker.PRINT)
                terminal.finish(0)
                assert '--output-dir' not in picker.printed_flags(terminal)
                assert json.loads(state_path(default_home, default_env).read_bytes())['output_dir'] == ''
                assert not (default_home/'Downloads').exists()
                assert len(server.requests) == before

            poison_home, poison_env = environment('poisoned-state')
            poison_path = state_path(poison_home, poison_env)
            poison_path.parent.mkdir(parents=True, mode=0o700)
            poison = b'{"version":999,"prompt":"SYNTHETIC_POISON_STATE"}\n'
            poison_path.write_bytes(poison)
            poison_path.chmod(0o600)
            direct_folder = root/'direct output'
            direct_folder.mkdir()
            for name, arguments, generates in [
                ('poisoned-direct-prompt', ['images', 'generate', '--prompt', 'Explicit synthetic prompt',
                                          '--output-dir', str(direct_folder), '--inline', 'off'], True),
                ('poisoned-direct-help', ['images', 'generate', '--help'], False),
                ('poisoned-direct-json', ['--format', 'json', 'images', 'generate', '--prompt', 'Structured synthetic prompt'], True),
            ]:
                before = len(server.requests)
                with terminal_case(name, poison_env, arguments, interactive=False) as terminal:
                    terminal.finish(0, expect_picker=False)
                    assert len(server.requests) == before+int(generates)
                    assert poison_path.read_bytes() == poison
                    assert 'Could not restore saved settings' not in terminal.text()
                    assert 'Could not remember these settings' not in terminal.text()
                    assert 'SYNTHETIC_POISON_STATE' not in terminal.text()
                    if generates:
                        assert server.requests[-1]['prompt'] == arguments[arguments.index('--prompt')+1]
                        assert 'output_dir' not in server.requests[-1]
                    if name == 'poisoned-direct-prompt':
                        check_images(direct_folder, 1)
                    elif name == 'poisoned-direct-json':
                        result = json.loads(terminal.text())
                        assert result['data'][0]['b64_json']

            before = len(server.requests)
            result = subprocess.run([binary, '--format', 'json', 'images', 'generate', '--prompt', 'Piped synthetic prompt'],
                                    input=b'', capture_output=True, env=poison_env, timeout=12)
            (output/'poisoned-json-pipe.stdout').write_bytes(result.stdout)
            (output/'poisoned-json-pipe.stderr').write_bytes(result.stderr)
            assert result.returncode == 0, (result.returncode, result.stderr)
            assert json.loads(result.stdout)['data'][0]['b64_json']
            assert not result.stderr, result.stderr
            assert len(server.requests) == before+1
            assert poison_path.read_bytes() == poison
            passed('poisoned-json-pipe')
        finally:
            server.release.set()
            server.shutdown()
            server.server_close()
            (output/'results.json').write_text(json.dumps(results, indent=2)+'\n')


if __name__ == '__main__':
    main()
