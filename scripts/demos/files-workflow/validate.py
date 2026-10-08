#!/usr/bin/env python3
"""Check synthetic requests, saved bytes, and complete before/after transcripts."""
import base64
import json
from pathlib import Path
import sys


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def main():
    check(len(sys.argv) == 2, 'usage: validate.py CAPTURE_DIRECTORY')
    root = Path(sys.argv[1])
    requests = [json.loads(line) for line in (root / 'requests.jsonl').read_text().splitlines()]
    check(len(requests) == 12, 'expected exactly six requests per scene')
    expected = [('POST', '/v1/files'), ('GET', '/v1/files/file-example'),
                ('GET', '/v1/files/file-example/content'), ('GET', '/v1/files/file-example/content'),
                ('GET', '/v1/files/file-binary/content'), ('GET', '/v1/files/file-binary/content')]
    check([(r['method'], r['path']) for r in requests] == expected * 2, 'unexpected request sequence')
    for request in (requests[0], requests[6]):
        check(request['content_type'].startswith('multipart/form-data; boundary='), 'upload was not multipart')
        check(request['filename'] == 'upload space.txt', 'multipart filename changed')
        check(request['purpose'] == 'user_data', 'explicit purpose changed')
        check(base64.b64decode(request['bytes'], validate=True) == b'hello files!\n', 'uploaded bytes changed')
    for scene in ['before', 'after']:
        files = root / f'{scene}-files'
        text = b'hello files!\n'
        binary = bytes([0,255,13,10,27,65,128,0,7,8,9,10])
        for name in ['upload space.txt', 'downloaded copy.txt', 'copy.txt']:
            check((files / name).read_bytes() == text, f'{scene}: {name} changed')
        for name in ['source.bin', 'copy.bin', 'redirected.bin']:
            check((files / name).read_bytes() == binary, f'{scene}: {name} changed')
        transcript = (root / f'{scene}.txt').read_text()
        for required in ['ID: file-example', 'Purpose: user_data',
                         'Text: both downloads match all 13 bytes.',
                         'Binary: both downloads match all 12 bytes.']:
            check(required in transcript, f'{scene}: missing {required}')
    before = (root / 'before.txt').read_text()
    after = (root / 'after.txt').read_text()
    check('Created at: 1700000000' in before, 'baseline timestamp changed')
    check('Uploaded upload space.txt (13 B)' in after, 'successful upload receipt missing')
    check('2023-11-14' in after, 'readable metadata timestamp missing')
    check('Download it: openai files download file-example' in after, 'download suggestion missing')
    print('PASS: 12 exact requests; two multipart filenames, purposes, and upload payloads')
    print('PASS: eight text/binary downloads match source bytes through destinations and shell redirection')
    print('PASS: both workflows exit zero; upload receipt, full ID, and readable timestamp appear after')


if __name__ == '__main__':
    main()
