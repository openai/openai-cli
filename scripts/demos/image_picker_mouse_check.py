#!/usr/bin/env python3
"""Interpret owned mouse modes through public synthetic picker lifecycle cases."""
import argparse
import fcntl
import shutil
import struct
import hashlib
import json
import os
import pathlib
import re
import signal
import sys
import tempfile
import termios
import time

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from image_picker_expanded_check import view_images
from image_picker_generation_check import SplitProcess, environment, fixture, saved, screen

MODES = (1000, 1002, 1003, 1006)
CONTROL = re.compile(rb'\x1b\[\?([0-9]+)([hl])')


def mouse_state(raw, original):
    state = dict(original)
    for match in CONTROL.finditer(raw):
        mode = int(match[1])
        if mode in MODES:
            state[mode] = match[2] == b'h'
    return state


def run(binary, output, case):
    output.mkdir()
    api = fixture('success')
    process = None
    original = {mode: False for mode in MODES}
    if case == 'inherited-encoding':
        original[1006] = True
    if case == 'inherited-tracking':
        original[1002] = original[1006] = True
    try:
        with tempfile.TemporaryDirectory(prefix='mouse-picker-') as directory:
            home = pathlib.Path(directory)
            env = environment(home, api)
            env.pop('NO_COLOR', None)
            env['COLORTERM'] = 'truecolor'
            if case=='apple-terminal':
                env['TERM_PROGRAM']='Apple_Terminal'
            if case=='open-full':
                bindir=home/'bin';bindir.mkdir()
                opener=bindir/'open'
                opener.write_text("#!/bin/sh\n[ -z \"$OPENAI_API_KEY\" ] || exit 91\nprintf '%s\\n' \"$1\" >> \"$HOME/opened-file-uri\"\n/bin/sleep 0.2\n")
                opener.chmod(0o700)
                env['PATH']=str(bindir)+':/usr/bin:/bin'
            executable = binary
            if case == 'removed-executable':
                executable = home / 'copied-openai'
                shutil.copyfile(binary, executable)
                executable.chmod(0o700)
            process = SplitProcess(executable, env, stderr_pipe=True)
            width, height = 96, 28
            def frame():
                return screen(bytes(process.streams['stdout']), width, height)
            if case == 'apple-terminal':
                # A fresh, wrapping PTY starts at row 1. Two spaces from the
                # final column leave the startup probe at row 2, column 2.
                process.wait_for(lambda: b'\x1b[6n' in process.streams['stdout'])
                process.send(b'\x1b[2;2R')
            process.ready()
            assert (b'$p' not in process.streams['stdout']) if case=='apple-terminal' else all(('\x1b[?%d$p' % mode).encode() in process.streams['stdout'] for mode in MODES)
            if case != 'unknown-terminal':
                reports = ''.join('\x1b[?%d;%d$y' % (mode, 1 if original[mode] else 2) for mode in MODES)
                process.send(reports)
                process.pump(0.15)
            process.submit('SYNTHETIC mouse cleanup')
            process.wait_for(lambda: len(api.requests) == 1)
            api.gates[0].set()
            process.wait_for(lambda: len(saved(home)) == 1 and b'Saved in' in frame() and b'Ctrl+G generate' in frame())
            active = mouse_state(bytes(process.streams['stdout']), original)
            owned = case not in ('unknown-terminal', 'inherited-tracking','apple-terminal')
            assert active[1000] == owned
            if owned:
                assert active[1006]
            if case == 'removed-executable':
                executable.unlink()
            if case == 'resize':
                for width, height in [(40, 12), (96, 28)]:
                    fcntl.ioctl(process.fd, termios.TIOCSWINSZ, struct.pack('HHHH', height, width, 0, 0))
                    process.wait_for(lambda: b'Create image' in frame() and b'Ctrl+G generate' in frame())
                    process.pump(0.15)
            if case=='open-full':
                process.send(b'\x1b[Z');process.pump(0.2)
                process.send('oo')
                process.wait_for(lambda:(home/'opened-file-uri').exists())
                process.pump(0.4)
                addresses=(home/'opened-file-uri').read_text().splitlines()
                assert len(addresses)==1 and addresses[0].startswith('file:///')
                assert len(api.requests)==1
            scroll = b'\x1b[<64;10;20M\x1b[<65;10;20M' * 8
            if any(active[mode] for mode in (1000, 1002, 1003)):
                process.send(scroll)
            for _ in range(3):
                view_images(process)
                process.wait_for(lambda: b'Esc back' in frame())
                if owned:
                    process.send(scroll)
                process.send(b'\x1b')
                process.wait_for(lambda: b'Create image' in frame())
            if case in ('busy-cancel', 'generation-failure'):
                process.pump(0.9)
                process.send(b'\x1b')
                process.pump(0.15)
                process.send(' another')
                process.pump(0.1)
                process.send(b'\r')
                process.wait_for(lambda: len(api.requests) == 2)
            if case == 'generation-failure':
                api.failed_requests.add(2)
                api.gates[1].set()
                process.wait_for(lambda: b'Authentication failed' in frame() and b'Ctrl+G generate' in frame())
                assert len(saved(home)) == 1
            expected = 130
            if case == 'print-only':
                process.pump(0.9)
                process.send(b'\x10')
                expected = 0
            elif case in ('sigterm', 'sighup'):
                os.kill(process.pid, signal.SIGTERM if case == 'sigterm' else signal.SIGHUP)
                expected = 143 if case == 'sigterm' else 129
            else:
                process.send((scroll if owned else b'') + b'\x03' + (scroll if owned else b''))
            observations = []
            deadline = time.monotonic() + 8
            while process.status is None:
                assert time.monotonic() < deadline, "mouse cleanup exit timed out"
                process.pump(0.02)
                state = mouse_state(bytes(process.streams['stdout']), original)
                cooked = bool(termios.tcgetattr(process.fd)[3] & (termios.ICANON | termios.ECHO))
                observations.append(dict(cooked=cooked, state=state))
                if cooked:
                    assert state == original, 'owned mouse reporting survived input restoration'
            process.pump(0.1)
            assert process.status == expected
            assert mouse_state(bytes(process.streams['stdout']), original) == original
            assert len(saved(home)) == 1
            raw = bytes(process.streams['stdout'])
            assert b'\x1b[?1000s' not in raw and b'\x1b[?1000r' not in raw
            assert b'\x1b[?1006s' not in raw and b'\x1b[?1006r' not in raw
            (output / 'report.json').write_text(json.dumps(dict(passed=True, case=case, initial=original, final=mouse_state(raw, original), observations=observations, exit_status=process.status, requests=len(api.requests), binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), native_terminal=False), indent=2) + '\n')
    finally:
        for gate in api.gates:
            gate.set()
        if process is not None:
            for name, data in process.streams.items():
                (output / (name + '.bin')).write_bytes(data)
            process.close()
        api.close()


def repeated_shell(binary, output):
    output.mkdir()
    api = fixture('success')
    process = None
    original = {mode: False for mode in MODES}
    try:
        with tempfile.TemporaryDirectory(prefix='mouse-shell-reentry-') as directory:
            home = pathlib.Path(directory)
            wrapper = home / 'shell-observer'
            wrapper.write_text("#!/bin/zsh -f\n" +
                'for round in 1 2; do\n' +
                '  "$SYNTHETIC_BINARY" images generate\n' +
                '  result=$?\n' +
                '  printf "\\nSYNTHETIC CLI %s EXIT %s\\n" "$round" "$result"\n' +
                '  [[ "$result" == 130 ]] || exit 19\n' +
                '  if [[ "$round" == 1 ]]; then\n' +
                '    IFS= read -r reply\n' +
                '    [[ "$reply" == NEXT ]] || exit 20\n' +
                '  fi\n' +
                'done\n' +
                'printf "SYNTHETIC SHELL READY\\n"\n' +
                'IFS= read -r reply\n' +
                'printf "SYNTHETIC SHELL INPUT <%s>\\n" "$reply"\n' +
                '[[ "$reply" == "SYNTHETIC shell command" ]] || exit 21\n')
            wrapper.chmod(0o700)
            env = environment(home, api)
            env['SYNTHETIC_BINARY'] = str(binary)
            env.pop('NO_COLOR', None)
            env['COLORTERM'] = 'truecolor'
            process = SplitProcess(wrapper, env, stderr_pipe=True)
            for round_number in (1, 2):
                process.wait_for(lambda: bytes(process.streams['stdout']).count(b'\x1b[?1000$p') == round_number)
                process.wait_for(lambda: b'Create image' in process.streams['stdout'])
                process.send(b'\x1b[?1000;2$y\x1b[?1002;2$y\x1b[?1003;2$y\x1b[?1006;2$y')
                process.pump(0.95)
                process.submit('SYNTHETIC scrolling')
                process.wait_for(lambda: len(api.requests) == round_number)
                api.gates[round_number - 1].set()
                process.wait_for(lambda: len(saved(home)) == round_number and b'Saved in' in screen(bytes(process.streams['stdout'])) and b'Ctrl+G generate' in screen(bytes(process.streams['stdout'])))
                assert mouse_state(bytes(process.streams['stdout']), original)[1000]
                burst = b'\x1b[<64;10;20M\x1b[<65;10;20M' * 20
                for _ in range(3):
                    process.send(burst)
                    process.pump(0.05)
                process.send(burst + b'\x03' + burst)
                marker = ('SYNTHETIC CLI %d EXIT 130' % round_number).encode()
                process.wait_for(lambda: marker in process.streams['stdout'])
                assert mouse_state(bytes(process.streams['stdout']), original) == original
                if round_number == 1:
                    process.send(b'NEXT\n')
            process.wait_for(lambda: b'SYNTHETIC SHELL READY' in process.streams['stdout'])
            # A physical wheel produces no input when the receiver's tracking modes are off.
            assert not any(mouse_state(bytes(process.streams['stdout']), original).values())
            process.send(b'SYNTHETIC shell command\n')
            process.wait_for(lambda: process.status is not None)
            assert process.status == 0
            assert b'SYNTHETIC SHELL INPUT <SYNTHETIC shell command>' in process.streams['stdout']
            assert len(saved(home)) == 2 and len(api.requests) == 2
            (output / 'report.json').write_text(json.dumps(dict(passed=True, launches=2, same_tty=True, queued_scroll_reports=240, shell_input='SYNTHETIC shell command', exit_status=0, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(), native_terminal=False), indent=2) + '\n')
    finally:
        for gate in api.gates:
            gate.set()
        if process is not None:
            for name, data in process.streams.items():
                (output / (name + '.bin')).write_bytes(data)
            if process.status is None:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    # Normal exit can remove the process group before teardown.
                    pass
            process.close()
        api.close()


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary', type=pathlib.Path)
    parser.add_argument('output', type=pathlib.Path)
    args = parser.parse_args()
    args.output.mkdir()
    for case in ('quit', 'busy-cancel', 'print-only', 'sigterm', 'sighup', 'inherited-encoding', 'inherited-tracking', 'unknown-terminal', 'generation-failure', 'resize', 'removed-executable','apple-terminal','open-full'):
        run(args.binary.resolve(), args.output / case, case)

    repeated_shell(args.binary.resolve(), args.output / 'repeated-shell')
