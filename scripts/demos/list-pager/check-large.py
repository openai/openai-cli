#!/usr/bin/env python3
"""Verify one synthetic 150-file pager through the public CLI."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import shutil
import time

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('large_pager', HERE/'observe.py')
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)
IDS = re.compile(r'\bfile-demo-[0-9]{3}\b')


def changed(session, previous):
    deadline = time.monotonic() + 8
    while True:
        frame = session.visible()
        if frame != previous and IDS.search(frame) and 'q: quit' in frame and 'Loading' not in frame:
            return frame
        assert session.terminal.child.poll() is None, 'pager exited during navigation'
        assert time.monotonic() < deadline, ('pager did not advance', frame)
        session.terminal.read()


def exercise(session, server):
    expected = [f'file-demo-{number:03d}' for number in range(1, 151)]
    session.wait_visible(expected[0])
    frame = session.wait_visible('q: quit')
    session.count(1)
    seen, frames = [], []
    checked_back = False
    for _ in range(40):
        found = IDS.findall(frame)
        assert found and len(found) == len(set(found)), ('duplicate visible row', found)
        assert all(f'dataset-{identifier[-3:]}.jsonl' in frame for identifier in found), frame
        new = [value for value in found if value not in seen]
        assert new == expected[len(seen):len(seen)+len(new)], ('missing or reordered files', len(seen), new)
        seen.extend(new)
        frames.append({'request_count': len(observer.requests(server)), 'ids': found, 'frame': frame})
        if 'Space: more' not in frame:
            break
        before = len(observer.requests(server))
        session.count(before)
        previous = frame
        session.send(' ')
        frame = changed(session, previous)
        after = len(observer.requests(server))
        assert after in (before, before+1), ('unexpected requests', before, after)
        if len(frames) == 1 and len(found) == server.rows:
            assert after == before+1, 'Space did not fetch the next page after all loaded rows were visible'
        if after != before:
            assert f'file-demo-{before*25:03d}' in seen, 'API page fetched before its loaded rows were visited'
        if after == 2 and not checked_back:
            session.send('b')
            assert changed(session, frame) == previous, 'back did not restore the previous chunk'
            session.count(2)
            session.send(' ')
            assert changed(session, previous) == frame, 'cached forward navigation changed the chunk'
            session.count(2)
            checked_back = True
    else:
        raise AssertionError('150-file fixture exceeded navigation bound')
    assert checked_back and seen == expected, (len(seen), len(expected))
    session.count(6)
    assert [request['page'] for request in observer.requests(server)] == list(range(1, 7))
    assert 'End' in frame and session.terminal.child.poll() is None, 'final page did not remain open'
    session.send(' ')
    session.count(6)
    assert session.visible() == frame, 'Space changed the final page'
    session.send('b')
    assert changed(session, frame) == frames[-2]['frame'], 'back failed on the final page'
    session.send(' ')
    assert changed(session, frames[-2]['frame']) == frame, 'cached final page changed'
    session.count(6)
    session.send('q')
    session.finish(0)
    (session.output/'coverage.json').write_text(json.dumps({
        'unique_records': len(seen), 'ids': seen, 'frames': frames,
        'lazy_requests': 6, 'maximum_inflight': server.maximum_inflight,
        'cached_back': checked_back, 'final_stays_open': True,
        'restored_original_history': True, 'status': 'PASS'}, indent=2)+'\n')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('binary', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--asciinema', required=True)
    parser.add_argument('--width', type=int, default=98)
    parser.add_argument('--height', type=int, default=20)
    args = parser.parse_args()
    observer.ASCIINEMA = args.asciinema
    args.output.mkdir(parents=True)
    before = hashlib.sha256(args.binary.read_bytes()).hexdigest()
    identity = {'path': str(args.binary.resolve()), 'sha256': before}
    (args.output/'binary.json').write_text(json.dumps(identity, indent=2)+'\n')
    source = args.output/'source'
    source.mkdir()
    for name in ('check-large.py', 'observe.py', 'fixture.py'):
        shutil.copy2(HERE/name, source/name)
    result = observer.run_case('files150', args.binary, args.output, exercise,
                               fixture_options={'pages': 6, 'rows': 25, 'numbered_files': True},
                               width=args.width, height=args.height)
    assert hashlib.sha256(args.binary.read_bytes()).hexdigest() == before, 'binary changed during verification'
    (args.output/'result.json').write_text(json.dumps({'binary': identity, 'check': result}, indent=2)+'\n')
    print('PASS: 150 unique files; six lazy requests; cached back; final page; terminal restoration')


if __name__ == '__main__':
    main()
