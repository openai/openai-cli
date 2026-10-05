#!/usr/bin/env python3
"""Check image loading output and cleanup through real process terminals."""
import json
import os
import pathlib
import re
import sys
import tempfile
import time

# Permit isolated Python (-I) without relying on the caller's import path.
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))
from terminal_test import run_terminal_test

if len(sys.argv) != 3:
    raise SystemExit('usage: check-image-loading.py MAIN_TEST OUTPUT_DIR')
binary = str(pathlib.Path(sys.argv[1]).resolve())
output = pathlib.Path(sys.argv[2]).resolve()
output.mkdir(parents=True, exist_ok=True)
labels = r'(?:Generating image|Editing image|Creating image variation)'
gated = {'generate', 'edit', 'variation', 'stdin', 'api-failure', 'malformed', 'save-failure', 'cancel',
         'stream', 'stream-interrupted', 'dumb', 'ci', 'utf8', 'no-color', 'ascii', 'locale-override'}
quiet = {'fast', 'json', 'error-json', 'debug', 'stdout-pipe', 'stderr-pipe', 'stdin-wait'}
partial_seen = {}
with tempfile.TemporaryDirectory(prefix='image-loading-gates-') as directory:
    env = {k: v for k, v in os.environ.items() if not k.startswith('OPENAI_')}
    env['OPENAI_CLI_LOADING_GATE_DIR'] = directory

    def observe(captured):
        text = captured.decode('utf-8', errors='replace')
        current = text.rsplit('MAIN-LOADING-CASE ', 1)[-1]
        name = current.split('\r\n', 1)[0]
        if name in gated and re.search(labels, current):
            (pathlib.Path(directory) / name).touch()
        if name in {'stream', 'stream-interrupted'} and 'Progress preview 1 of 1:' in current:
            after = current.split('Progress preview 1 of 1:', 1)[1]
            assert not re.search(labels, after), (name, 'spinner restarted after progress')
            partial_seen.setdefault(name, time.monotonic())
            if time.monotonic() - partial_seen[name] >= 0.25:
                (pathlib.Path(directory) / (name + '-partial')).touch()

    code, captured = run_terminal_test(binary, 'TestMainImageLoadingFeedbackTerminal', env,
                                      observe, timeout=90)

(output / 'loading-pty.raw').write_bytes(captured)
text = captured.decode('utf-8', errors='replace')
assert code == 0 and '--- SKIP' not in text, text
results = {}
for name in sorted(gated | quiet):
    section = text.split('MAIN-LOADING-CASE ' + name + '\r\n', 1)[1].split(
        'MAIN-LOADING-END ' + name + '\r\n', 1)[0]
    count = len(re.findall(labels, section))
    if name in quiet:
        assert count == 0, (name, 'unexpected feedback', section)
    else:
        assert count >= 1, (name, 'missing delayed feedback', section)
        if name != 'cancel':
            assert section.index(re.search(labels, section).group()) < section.index(
                'LOADING-RESPONSE-SEND ' + name), (name, 'buffered feedback')
    if name in {'dumb', 'ci'}:
        assert count == 1 and '\x1b[2K' not in section, (name, 'expected static feedback')
    if name in {'utf8', 'no-color'}:
        assert '⣾' in section, (name, 'missing Unicode spinner')
        assert not re.search(r'\x1b\[[0-9;]*m', section), (name, 'loading must preserve the terminal foreground')
    if name in {'ascii', 'locale-override'}:
        assert '\r| Generating image' in section, (name, 'missing ASCII fallback')
        assert '⣾' not in section and '\x1b[36m' not in section
    if name == 'json':
        payload = section.split('LOADING-RESPONSE-SEND json\r\n', 1)[1].strip()
        assert isinstance(json.loads(payload)['data'], list), 'API output is not clean JSON'
    if name == 'error-json':
        payload = section.split('LOADING-RESPONSE-SEND error-json\r\n', 1)[1].strip()
        assert json.loads(payload)['type'] == 'invalid_request_error', 'error output is not clean JSON'
    for marker in ['Progress preview 1 of 1:', 'Saved image:']:
        if marker in section:
            assert not re.search(labels, section.split(marker, 1)[1]), (name, 'late redraw')
    assert '\x1b[?25l' not in section, (name, 'cursor hidden')
    results[name] = {'loading_frames': count, 'passed': True}
(output / 'loading-pty-results.json').write_text(json.dumps(results, indent=2) + '\n')
print(json.dumps(results, indent=2))
