#!/usr/bin/env python3
"""Read-only synthetic inventory fixture for the shared demo recorder."""
import json
import signal
import sys
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == '/v1/organization/admin_api_keys':
            data = []
        elif self.path == '/v1/organization/projects/proj_demo/api_keys':
            data = [{
                'object': 'organization.project.api_key',
                'redacted_value': 'synthetic...demo',
                'name': 'Build automation',
                'created_at': 1791500000,
                'expires_at': 1794092000,
                'last_used_at': None,
                'id': 'key_demo_complete_id',
                'owner_project_access': 'inactive',
                'owner': {'type': 'service_account', 'service_account': {
                    'id': 'svc_demo', 'name': 'Build runner', 'role': 'none'
                }},
            }]
        else:
            self.send_error(404)
            return
        payload = json.dumps({'object': 'list', 'data': data, 'has_more': False}).encode()
        with open(sys.argv[2], 'a', encoding='utf-8') as log:
            log.write(json.dumps({'method': 'GET', 'path': self.path}) + '\n')
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_):
        pass


server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
signal.signal(signal.SIGTERM, lambda *_: threading.Thread(target=server.shutdown).start())
Path(sys.argv[1]).write_text(f'http://127.0.0.1:{server.server_port}/v1', encoding='utf-8')
try:
    server.serve_forever()
finally:
    server.server_close()
