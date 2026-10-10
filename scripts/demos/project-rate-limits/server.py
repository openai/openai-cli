#!/usr/bin/env python3
"""Loopback-only synthetic project rate limits. Never contacts the live API."""
import http.server
import json
import pathlib
import signal
import sys
import threading
import urllib.parse

record = {"id": "rl_demo", "object": "project.rate_limit", "model": "model_demo",
          "max_requests_per_1_minute": 100, "max_tokens_per_1_minute": 1000,
          "max_requests_per_1_day": 5000, "batch_1_day_max_input_tokens": 20000}
failures = []
requests = []


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
            failures.append("unexpected credentials")
            self.send_error(403)
            return
        if path not in ("/v1/organization/projects/proj_demo/rate_limits",
                        "/v1/organization/projects/proj_empty/rate_limits"):
            failures.append("unexpected route")
            self.send_error(404)
            return
        requests.append(path)
        payload = {"object": "list", "data": [] if "proj_empty" in path else [record],
                   "has_more": False}
        data = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)
        self.wfile.flush()

    def log_message(self, *_):
        pass


if len(sys.argv) != 2:
    raise SystemExit("usage: server.py ADDRESS_FILE")
class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True

    def handle_error(self, *_):
        failures.append("request failed")


server = Server(("127.0.0.1", 0), Handler)


def stop(*_):
    threading.Thread(target=server.shutdown).start()


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
try:
    pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}/v1")
    server.serve_forever()
finally:
    server.server_close()
if failures:
    raise SystemExit("Synthetic fixture rejected unexpected requests")
if sorted(requests) != sorted([
    '/v1/organization/projects/proj_demo/rate_limits',
    '/v1/organization/projects/proj_demo/rate_limits',
    '/v1/organization/projects/proj_empty/rate_limits',
]):
    raise SystemExit("Synthetic fixture received an unexpected request count")
