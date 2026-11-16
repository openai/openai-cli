#!/usr/bin/env python3
"""Validate the colored workflow, request boundary, and byte-preserving relay."""
import hashlib
import json
import pathlib
import sys

output = pathlib.Path(sys.argv[1])
requests = [json.loads(line) for line in (output/'requests.jsonl').read_text().splitlines()]
assert [(r['scene'], r['operation'], r['api_status']) for r in requests] == [
    ('workflow', 'catalog', 200), ('workflow', 'list', 200), ('workflow', 'create', 200)]
assert (output/'workflow.status').read_text() == '0\n'
evidence = json.loads((output/'workflow-evidence.json').read_text())
assert evidence['exit_status'] == 0 and (evidence['width'], evidence['height']) == (80, 24)
assert list(evidence['checkpoints']) == ['form', 'category', 'search', 'selection', 'events', 'endpoints',
    'endpoint-details', 'draft-return', 'confirmation', 'confirm-yes']
screens = evidence['screens']
assert '[No]   Yes, create' in screens['confirmation']
assert '[Yes, create]' in screens['confirm-yes']
assert all(value in screens['endpoints'] for value in ('Response notifications', 'Batch updates', 'Video jobs'))
assert all(value in screens['endpoint-details'] for value in ('https://example.com/responses', 'response.completed', 'response.failed'))
assert all(value in screens['draft-return'] for value in ('Response notifications', 'https://example.com/webhook', '[x] response.completed', '[x] response.failed'))
raw = (output/'workflow-evidence.tty').read_bytes()
assert hashlib.sha256(raw).hexdigest() == evidence['relayed_sha256']
assert b'\x1b[38;2;' in raw, 'truecolor output missing'
child = [json.loads(line) for line in (output/'workflow-evidence.child.cast').read_text().splitlines()]
assert ''.join(e[2] for e in child[1:] if e[1] == 'o').encode() == raw
outer = [json.loads(line) for line in (output/'workflow.cast').read_text().splitlines()]
assert (outer[0]['width'],outer[0]['height']) == (80,28)
capture = ''.join(e[2] for e in outer[1:] if e[1] == 'o')
marker = '$ openai webhooks create\r\n'
footer = '\r\nTerminal replay | synthetic data\r\n$ '
assert capture.count(marker) == 1 and capture.endswith(footer)
assert capture.split(marker,1)[1][:-len(footer)].encode() == raw
child_start = capture.index(marker) + len(marker)
consumed = 0
for event in outer[1:]:
    if event[1] == 'o':
        consumed += len(event[2])
        if consumed > child_start:
            offset = event[0] - child[1][0]
            break
(output/'workflow-frame-times.json').write_text(json.dumps({
    name: when + offset - .1 for name, when in evidence['checkpoints'].items()
},indent=2)+'\n')
print('PASS: colored creation, category/search selection, endpoint list/details, retained draft, and default-No confirmation.')
print('PASS: exactly three synthetic requests, gated creation, exit zero, and unchanged terminal relay bytes.')
print('Visual inspection remains required for spacing, clipping, and timing.')
