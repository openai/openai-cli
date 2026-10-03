#!/usr/bin/env python3
"""Exercise installed profile blocks in native shells using temporary homes.

Run with python3 -I -B scripts/check-image-picker-setup.py BINARY OUTPUT.
No user profiles, package installations, or API calls are used.
"""
import argparse
import importlib.util
import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import time

spec = importlib.util.spec_from_file_location(
    'picker_harness', pathlib.Path(__file__).with_name('image_picker_harness.py'))
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('output')
    args = parser.parse_args()
    binary = pathlib.Path(args.binary).resolve()
    output = pathlib.Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    results = []
    required = set(os.environ.get('OPENAI_CLI_REQUIRE_NATIVE_SHELLS', '').split(','))
    with tempfile.TemporaryDirectory(prefix='image-picker-setup-') as temporary:
        root = pathlib.Path(temporary)
        bindir = root/'bin'
        bindir.mkdir()
        (bindir/'openai').symlink_to(binary)
        shells = [('bash', shutil.which('bash')), ('zsh', shutil.which('zsh')),
                  ('fish', shutil.which('fish'))]
        if pathlib.Path('/bin/bash').exists() and shells[0][1] != '/bin/bash':
            shells.append(('bash-legacy', '/bin/bash'))
        for label, executable in shells:
            shell = label.split('-')[0]
            if executable is None:
                if shell in required:
                    raise AssertionError(f'required native {shell} is unavailable')
                results.append({'shell': label, 'result': 'skip', 'reason': 'shell unavailable'})
                continue
            home = root/(label+" home with ' quotes")
            home.mkdir()
            profile = home/"startup with ' quotes"
            original = b'# Synthetic user-owned startup content\n'
            profile.write_bytes(original)
            profile.chmod(0o600)
            env = {'PATH': str(bindir)+':/usr/bin:/bin', 'HOME': str(home),
                   'XDG_CONFIG_HOME': str(home/"config with ' quotes"),
                   'TERM': 'xterm-256color', 'LANG': 'en_US.UTF-8', 'CI': 'true',
                   'OPENAI_BASE_URL': 'http://127.0.0.1:1',
                   'OPENAI_TEST_PROFILE': str(profile)}

            def setup(action):
                result = subprocess.run([str(binary), '@completion', shell, action,
                                         '--profile', str(profile)], capture_output=True,
                                        env=env, timeout=10)
                assert result.returncode == 0 and not result.stderr, (result.returncode, result.stderr)
                return result.stdout

            setup('--install-picker')
            installed, identity = profile.read_bytes(), profile.stat()
            assert installed.startswith(original) and installed.count(b'# >>> openai image picker') == 1
            setup('--install-picker')
            assert profile.read_bytes() == installed
            assert profile.stat().st_ino == identity.st_ino and profile.stat().st_mtime_ns == identity.st_mtime_ns

            if shell == 'bash':
                command = '. "$OPENAI_TEST_PROFILE"; complete -p openai; '
                command += 'printf "INTEGRATION:%s\\n" "${OPENAI_PICKER_INTEGRATION-}"'
                shell_args = ['--noprofile', '--norc', '-i', '-c', command]
            elif shell == 'zsh':
                command = 'source "$OPENAI_TEST_PROFILE"; '
                command += 'printf "COMPLETION:%s\\nINTEGRATION:%s\\n" "${_comps[openai]}" "${OPENAI_PICKER_INTEGRATION-}"'
                shell_args = ['-f', '-i', '-c', command]
            else:
                command = 'source "$OPENAI_TEST_PROFILE"; emit fish_prompt; functions -q openai_picker_disable; or exit 9; '
                command += 'printf "INTEGRATION:%s\\n" "$OPENAI_PICKER_INTEGRATION"'
                shell_args = ['--no-config', '-i', '-c', command]
            terminal = picker.Terminal(executable, shell_args, env)
            try:
                deadline = time.monotonic()+10
                while terminal.child.poll() is None:
                    terminal.read()
                    assert time.monotonic() < deadline, 'shell did not finish profile setup'
                for _ in range(3):
                    terminal.read(0.03)
                # A native shell may revoke its PTY on exit. Unlike a picker
                # child returning to that shell, it has no terminal to restore.
                assert terminal.child.returncode == 0, terminal.text()
                text = terminal.text()
                if shell == 'bash':
                    assert 'complete -o filenames -F __openai_' in text, text
                    version = subprocess.check_output([executable, '--version'], text=True).splitlines()[0]
                    if 'version 3.' in version or 'version 4.0.' in version or 'version 4.1.' in version or 'version 4.2.' in version:
                        assert 'INTEGRATION:bash' not in text, text
                    else:
                        assert 'INTEGRATION:bash' in text, text
                elif shell == 'zsh':
                    assert 'COMPLETION:__openai_zsh_autocomplete' in text and 'INTEGRATION:zsh' in text, text
                else:
                    assert 'INTEGRATION:fish' in text, text
            finally:
                terminal.save(output/label)
                terminal.close()

            setup('--uninstall-picker')
            assert profile.read_bytes() == original
            markers = list(home.rglob('image-picker.json.tab-off-'+shell))
            assert len(markers) == 1 and markers[0].read_bytes() == b'', markers
            assert not list(home.rglob('picker-'+shell+'-*')), 'removed setup left an owned script'
            setup('--install-picker')
            assert not markers[0].exists(), 'explicit installation did not clear opt-out'
            setup('--uninstall-picker')
            assert profile.read_bytes() == original
            results.append({'shell': label, 'executable': executable, 'result': 'pass'})
            print('PASS', label, flush=True)
    (output/'results.json').write_text(json.dumps(results, indent=2)+'\n')


if __name__ == '__main__':
    main()
