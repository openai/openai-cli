#!/usr/bin/env python3
"""Synthetic-only Skills API used by the shared demo recorder."""
import hashlib
import io
import json
import pathlib
import signal
import sys
import threading
import zipfile
from email.parser import BytesParser
from email.policy import default
from http.server import BaseHTTPRequestHandler, HTTPServer

address, log_path, source = map(pathlib.Path, sys.argv[1:])
expected = (source / 'demo-skill.zip').read_bytes()
expected_files = {p.relative_to(source).as_posix(): p.read_bytes()
                  for p in (source / 'demo-skill').rglob('*') if p.is_file()}
records = []

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        if self.headers.get('Authorization') != 'Bearer synthetic-demo-key':
            self.send_error(403)
            return
        if self.path not in ('/v1/skills', '/v1/skills/skill_demo/versions'):
            self.send_error(404)
            return
        body = self.rfile.read(int(self.headers.get('Content-Length', '0')))
        record = {'path': self.path, 'content_type': self.headers.get('Content-Type'), 'valid': False}
        try:
            message = BytesParser(policy=default).parsebytes(
                ('Content-Type: ' + self.headers['Content-Type'] + '\r\nMIME-Version: 1.0\r\n\r\n').encode() + body)
            if not message.is_multipart():
                decoded = json.loads(body)
                submitted = decoded['files'][0].encode('utf-8')
                record['zip_bytes_equal'] = submitted == expected
                raise ValueError('The uploaded archive bytes do not match the source ZIP.')
            parts = list(message.iter_parts())
            assert len(parts) == 1
            part = parts[0]
            assert part.get_param('name', header='content-disposition') == 'files'
            assert part.get_filename() == 'demo-skill.zip'
            submitted = part.get_payload(decode=True)
            archive = zipfile.ZipFile(io.BytesIO(submitted))
            entries = {p.filename: archive.read(p) for p in archive.infolist() if not p.is_dir()}
            assert entries == expected_files
            assert archive.getinfo('demo-skill/helper.sh').external_attr >> 16 & 0o111
            record.update(valid=True, sha256=hashlib.sha256(submitted).hexdigest(),
                          zip_bytes_equal=submitted == expected, entries=sorted(entries))
            response = {'id': 'skill_demo', 'object': 'skill', 'name': 'demo-skill',
                        'default_version': '1', 'latest_version': '1'}
            if self.path.endswith('/versions'):
                response = {'id': 'version_demo', 'object': 'skill.version', 'skill_id': 'skill_demo', 'version': '2'}
            status = 200
        except (ValueError, AssertionError, KeyError, zipfile.BadZipFile):
            response = {'error': {'type': 'invalid_request_error', 'message': 'The uploaded archive bytes are invalid.', 'param': 'files'}}
            status = 400
        records.append(record)
        payload = json.dumps(response).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
        self.wfile.flush()

server = HTTPServer(('127.0.0.1', 0), Handler)
signal.signal(signal.SIGTERM, lambda *_: threading.Thread(target=server.shutdown).start())
address.write_text('http://127.0.0.1:%d/v1' % server.server_address[1])
try:
    server.serve_forever()
finally:
    server.server_close()
    with log_path.open('x') as log:
        for record in records:
            log.write(json.dumps(record) + '\n')
