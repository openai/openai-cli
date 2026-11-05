#!/usr/bin/env python3
"""Loopback-only, synthetic project lifecycle fixture. No external API calls."""

import json
import pathlib
import signal
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    failures = []
    stopping = False
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                try:
                    if self.headers.get("Authorization") != "Bearer synthetic-admin-key":
                        raise ValueError("unexpected fixture credentials")
                    length = int(self.headers.get("Content-Length", "0"))
                    if not 0 <= length <= 1024:
                        raise ValueError("unexpected fixture body size")
                    body = json.loads(self.rfile.read(length) or b"{}")
                    expected = {
                        "/v1/organization/projects": {"name": "Demo", "residency": "GLOBAL"},
                        "/v1/organization/projects/proj_demo": {"name": "Renamed"},
                        "/v1/organization/projects/proj_demo/archive": {},
                    }
                    if self.path not in expected or body != expected[self.path]:
                        raise ValueError("unexpected fixture request")
                    log.write(json.dumps({"method": "POST", "path": self.path, "body": body}) + "\n")
                    log.flush()
                    archived = self.path.endswith("/archive")
                    result = {
                        "id": "proj_demo", "object": "organization.project",
                        "name": "Demo" if self.path.endswith("/projects") else "Renamed",
                        "status": "archived" if archived else "active", "residency": "GLOBAL",
                    }
                    response = json.dumps(result).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(response)))
                    self.end_headers()
                    self.wfile.write(response)
                    self.wfile.flush()
                except Exception as error:
                    failures.append(error)
                    self.send_error(500, "synthetic fixture failed")

        with HTTPServer(("127.0.0.1", 0), Handler) as server:
            server.timeout = 0.2

            def stop(_signal, _frame):
                nonlocal stopping
                stopping = True

            signal.signal(signal.SIGTERM, stop)
            signal.signal(signal.SIGINT, stop)
            pathlib.Path(sys.argv[1]).write_text(
                f"http://127.0.0.1:{server.server_port}/v1", encoding="utf-8"
            )
            while not stopping:
                server.handle_request()
    if failures:
        raise SystemExit("synthetic fixture failed")


if __name__ == "__main__":
    main()
