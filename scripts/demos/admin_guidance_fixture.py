#!/usr/bin/env python3
"""A loopback-only project list for the admin setup recording."""
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
import signal
import sys

requests = []
running = True


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        authenticated = self.headers.get("Authorization") == "Bearer sk-admin-SYNTHETIC-demo-only"
        expected_route = "/v1/organization/projects?limit=1"
        valid = self.path == expected_route and authenticated
        route = expected_route if self.path == expected_route else "unexpected route"
        requests.append({"method": "GET", "path": route, "authenticated": authenticated, "status": 200 if valid else 403})
        data = {"object": "list", "data": [{"id": "proj_synthetic", "object": "organization.project", "name": "Synthetic demo project", "created_at": 1700000000, "status": "active"}], "first_id": "proj_synthetic", "last_id": "proj_synthetic", "has_more": False}
        body = json.dumps(data if valid else {"error": {"message": "Synthetic fixture rejected the request."}}).encode()
        self.send_response(200 if valid else 403)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_):
        pass


def stop(*_):
    global running
    running = False


if __name__ == "__main__":
    address, log = map(Path, sys.argv[1:])
    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with HTTPServer(("127.0.0.1", 0), Handler) as server:
        server.timeout = 0.1
        address.write_text(f"http://127.0.0.1:{server.server_port}/v1")
        while running:
            server.handle_request()
    log.write_text(json.dumps(requests, indent=2) + "\n")
