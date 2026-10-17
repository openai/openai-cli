#!/usr/bin/env python3
"""Loopback-only fixture for the option-scope recovery replay."""
import http.server
import json
from pathlib import Path
import signal
import sys
import threading

requests = []

class Handler(http.server.BaseHTTPRequestHandler):
    def parse_request(self):
        parsed = super().parse_request()
        if parsed:
            requests.append(self.command == "GET" and self.path == "/files?limit=1")
        return parsed

    def do_GET(self):
        expected = self.command == "GET" and self.path == "/files?limit=1"
        if not expected:
            self.send_error(400, "unexpected synthetic request")
            return
        body = json.dumps({"object": "list", "data": [{"id": "file_demo", "object": "file"}], "has_more": False}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    do_POST = do_GET
    do_PUT = do_GET
    do_PATCH = do_GET
    do_DELETE = do_GET

    def log_message(self, *_):
        pass

server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
server.daemon_threads = True
signal.signal(signal.SIGTERM, lambda *_: threading.Thread(target=server.shutdown, daemon=True).start())
try:
    Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}")
    server.serve_forever()
finally:
    server.server_close()
    Path(sys.argv[2]).write_text(json.dumps({"expected_gets": requests.count(True), "unexpected_requests": requests.count(False)}) + "\n")
