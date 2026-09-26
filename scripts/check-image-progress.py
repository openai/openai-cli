#!/usr/bin/env python3
"""Run terminal-only regression groups in a real sized PTY, offline."""
import errno
import fcntl
import json
import os
import pathlib
import pty
import re
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

if len(sys.argv) != 4:
    raise SystemExit('usage: check-image-progress.py CUSTOM_TEST MAIN_TEST OUTPUT_DIR')
custom_binary, main_binary, destination = sys.argv[1:]
root = pathlib.Path(destination).resolve()
root.mkdir(parents=True, exist_ok=True)
binaries = {'custom': str(pathlib.Path(custom_binary).resolve()), 'main': str(pathlib.Path(main_binary).resolve())}
results = {}
gates = tempfile.TemporaryDirectory(prefix='image-progress-gates-')
for binary, test, prefix in [('custom', 'TestImageProgressTerminal', 'PROGRESS'), ('main', 'TestMainImageProgressTerminal', 'MAIN-PROGRESS')]:
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 45, 120, 0, 0))
    env = {k: v for k, v in os.environ.items() if not k.startswith('OPENAI_')}
    env['OPENAI_CLI_PROGRESS_GATE_DIR'] = gates.name
    child = subprocess.Popen([binaries[binary], '-test.v', '-test.run=^' + test + '$'], stdin=subprocess.DEVNULL, stdout=slave, stderr=slave, env=env)
    os.close(slave)
    captured = bytearray()
    deadline = time.monotonic() + 60
    while True:
        if time.monotonic() > deadline:
            child.kill()
            raise AssertionError('terminal tests timed out')
        if select.select([master], [], [], 0.2)[0]:
            try:
                chunk = os.read(master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    break
                raise
            if not chunk:
                break
            captured.extend(chunk)
            if binary == 'main':
                current = captured.decode('utf-8', errors='replace').rsplit('MAIN-PROGRESS-CASE ', 1)[-1]
                name = current.split('\r\n', 1)[0]
                if len(re.findall(r'Progress preview [12] of 2:', current)) == 2 and name in ('generate','edit','stdin','iterm','failure'):
                    (pathlib.Path(gates.name) / name).touch()
        elif child.poll() is not None:
            break
    os.close(master)
    code = child.wait()
    (root / (binary + '-pty.raw')).write_bytes(captured)
    text = captured.decode('utf-8')
    assert code == 0 and '--- SKIP' not in text, text
    expected = {'kitty':2,'iterm':2,'apple':2,'off':0,'ci':0,'no-color':0,'malformed':0,'malformed-error':0,'malformed-cancel':0,'invalid-index':0,'stream-error':2,'cancel':2} if binary == 'custom' else {'generate':2,'edit':2,'stdin':2,'iterm':2,'off':0,'ci':0,'api':0,'malformed':0,'malformed-error-json':0,'failure':2,'final-first':0}
    for name, count in expected.items():
        start = text.index(prefix + '-CASE ' + name + '\r\n')
        end = text.index(prefix + '-END ' + name + '\r\n', start)
        section = text[start:end]
        assert len(re.findall(r'Progress preview [12] of 2:', section)) == count, (binary,name,section)
        assert section.count('Progress preview unavailable;') == (1 if name.startswith('malformed') else 0), (binary,name,section)
        if binary == 'main' and count:
            final_send = section.index('PROGRESS-FINAL-SEND ' + name)
            assert section.rindex('Progress preview ') < final_send, (name,'buffered progress')
        if binary == 'main' and name == 'api':
            assert len(re.findall(r'"partial_image_index":\s*0', section)) == 2
            assert '"image_generation.completed"' in section and 'Saved image:' not in section
        if binary == 'custom' and name == 'apple':
            assert '▀' in section and '\x1b_G' not in section and '\x1b]1337;' not in section
    assert '\x1b_G' in text and '\x1b]1337;' in text
    results[binary] = {'passed': len(expected), 'exit_code':code, 'bytes':len(captured), 'skipped':False}
(root / 'pty-results.json').write_text(json.dumps(results, indent=2) + '\n')
print(json.dumps(results, indent=2))
gates.cleanup()
