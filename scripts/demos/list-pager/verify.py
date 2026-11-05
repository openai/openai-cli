#!/usr/bin/env python3
"""Verify exact bytes, reviewed inputs, rendered history, and fixture requests."""
import hashlib
import json
from pathlib import Path
import re
import sys

output = Path(sys.argv[1])
reports = {}
binaries = json.loads((output / 'binaries.json').read_text())
for label, entry in binaries.items():
    assert hashlib.sha256(Path(entry['path']).read_bytes()).hexdigest() == entry['sha256'], (label, 'input changed')
assert binaries['before']['sha256'] == '211e3de6133d72acd4fc604d13a23d4cc437e7980969d86d8cc01a530fc15d0b'
reports['binaries'] = binaries
for version in ('before', 'after'):
    cast = [json.loads(line) for line in (output / f'{version}.cast').read_text().splitlines()]
    assert (cast[0]['width'], cast[0]['height']) == (98, 20)
    captured = ''.join(event[2] for event in cast[1:] if event[1] == 'o')
    marker = '$ openai files list --limit 1 --max-items -1' + '\r\n'
    assert captured.count(marker) == 1, 'ambiguous command boundary'
    tail = captured.split(marker, 1)[1]
    assert tail.count('$ ') == 1, 'ambiguous final prompt'
    relayed, prompt_tail = tail.split('$ ', 1)
    assert prompt_tail == '', 'unexpected bytes after final prompt'
    raw = relayed.encode()
    evidence = json.loads((output / f'{version}.relay.json').read_text())
    assert evidence['exit_status'] == 0, evidence
    assert hashlib.sha256(raw).hexdigest() == evidence['sha256'], 'relay bytes changed'
    keys = [item['key'] for item in evidence['inputs']]
    assert keys == ([' '] if version == 'before' else [' ', 'b', ' ', 'q']), keys
    assert not re.search(rb'\x1b\[\?(?:2026|2027)\$p', raw), 'unsupported terminal mode query'
    assert b'\x1b[38;2;138;168;255mID' in raw, 'blue ID header missing'
    if version == 'before':
        assert b'\x1b[38;2;164;172;194m' in raw, 'muted hint missing'
    else:
        assert b'Full data:' not in raw, 'resource hint created a pager-only scroll step'
    entered, alternate = False, False
    for control in re.finditer(rb'\x1b\[\?(?:47|1047|1049)([hl])|\x1b\[([23])J', raw):
        mode, erase = control.groups()
        if mode:
            alternate = mode == b'h'
            entered = entered or alternate
        else:
            assert erase == b'2' and alternate, 'original screen or history cleared'
    assert entered == (version == 'after') and not alternate, 'alternate screen policy or restoration failed'
    visible = (output / f'{version}.txt').read_text()
    for page in (1, 2):
        identifier = f'file-demo--{page:03d}-001'
        assert visible.count(identifier) == (1 if version == 'before' else 0), ('history restoration mismatch', identifier, visible)
    assert all(marker not in visible for marker in ('Space: more', 'b: back', 'q: quit', 'i: inline', 'Loading')), visible
    assert not re.search(r'(?m)^\s*End(?:\s|$)', visible), visible
    assert visible.count('Synthetic loopback API') == 1, 'original screen was not restored'
    assert visible.rstrip().endswith('$'), 'final shell prompt missing'
    (output / f'{version}.raw').write_bytes(raw)
    reports[version] = {'exact_relay_digest': evidence['sha256'], 'exit_status': 0, 'keys': keys,
                        'expected_final_history': True, 'final_footer_absent': True, 'shell_prompt': True}
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
expected = [{'page': page, 'status': 200, 'cursor_valid': True} for _ in range(2) for page in (1, 2)]
assert requests == expected, requests
reports['requests'] = requests
final_page = (output/'after-final-page.txt').read_text()
assert final_page.count('file-demo--002-001') == 1, final_page
assert 'file-demo--001-001' not in final_page, final_page
assert 'b: back' in final_page and 'q: quit' in final_page, final_page
assert 'i: inline' not in final_page and 'p: print' not in final_page, final_page
reports['final_page'] = {'second_page_visible': True, 'waits_for_quit': True, 'back_available': True}
reports['scope'] = 'Actual PTY replay. Native terminal appearance and clipboard behavior remain unverified.'
(output / 'validation.json').write_text(json.dumps(reports, indent=2) + '\n')
print('PASS: exact relay, intended keys, lazy requests, restored screen, blue headers, restored history, final shell prompt')
