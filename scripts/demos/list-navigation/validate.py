#!/usr/bin/env python3
"""Validate request timing, exit statuses, and captured terminal output."""
import json
import pathlib
import sys


def main():
    if len(sys.argv) != 2:
        raise SystemExit('usage: validate.py OUTPUT_DIR')
    output = pathlib.Path(sys.argv[1])
    requests = [json.loads(line) for line in (output/'requests.jsonl').read_text().splitlines()]
    assert len(requests) == 4, requests
    comparison_settings = None
    for scene in ('before', 'after'):
        records = [request for request in requests if request['scene'] == scene]
        assert [record['page'] for record in records] == [1, 2], records
        assert [record['cursor'] for record in records] == ['', 'file_002'], records
        assert all(record['limit'] == 2 for record in records)
        assert records[0]['received_unix_nano'] < records[1]['received_unix_nano']
        evidence = json.loads((output/(scene+'-evidence.json')).read_text())
        assert evidence['command'] == 'openai files list --limit 2 --max-items -1'
        assert evidence['exit_status'] == 0
        if comparison_settings is None:
            comparison_settings = evidence['settings']
        assert evidence['settings'] == comparison_settings, 'before and after settings differ'
        milestones = {item['name']: item for item in evidence['milestones']}
        idle = milestones['idle-before-input']
        assert idle['elapsed_seconds']-milestones['first-visible']['elapsed_seconds'] >= 1.4
        assert idle['requests'] == (2 if scene == 'before' else 1)
        assert milestones['finished']['requests'] == 2
        if scene == 'before':
            assert evidence['keys'] == []
        else:
            assert [item['key'] for item in evidence['keys']] == ['Space', 'q']
            assert evidence['keys'][0]['elapsed_seconds'] >= idle['elapsed_seconds']
            assert milestones['after-space']['requests'] == milestones['last-page-idle']['requests'] == 2
        # The driver reconstructs these frames with asciinema. Raw output can
        # contain only a changed digit instead of the complete next file ID.
        screens = evidence['screens']
        for number in (1, 2):
            assert f'file_{number:03d}' in screens['first-page'], (scene, 'first page', number)
        final_page = screens['last-page'] if scene == 'after' else screens['finished']
        for number in (3, 4):
            assert f'file_{number:03d}' in final_page, (scene, 'last page', number)
        cast = [json.loads(line) for line in (output/(scene+'.cast')).read_text().splitlines()]
        assert cast[0]['version'] == 2 and cast[0]['width'] == 90 and cast[0]['height'] == 30
        # capture_and_render.sh creates this transcript with asciinema convert.
        # Use its reconstructed output instead of stripping terminal controls.
        rendered = (output/(scene+'.txt')).read_text()
        assert 'synthetic loopback API' in rendered
        assert '$ openai files list --limit 2 --max-items -1' in rendered
        for number in (3, 4):
            assert f'file_{number:03d}' in rendered, (scene, number)
        assert ('Space: more' in screens['first-page']) == (scene == 'after')
        if scene == 'after':
            assert 'End of results' in final_page
        print(f'{scene}: two requests, exit 0, idle count {idle["requests"]}, captured all four synthetic files')


if __name__ == '__main__':
    main()
