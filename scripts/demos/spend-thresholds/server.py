#!/usr/bin/env python3
"""Serve fixed spend responses on loopback and fail on fixture I/O errors."""

import hashlib
import http.server
import json
import os
from pathlib import Path
import signal
import sys
from urllib.parse import urlsplit


def write_text(path, text):
    with Path(path).open("x", encoding="utf-8") as destination:
        if destination.write(text) != len(text):
            raise OSError("Incomplete fixture file write")


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_METADATA")
    limit = {"object": "organization.spend_limit", "threshold_amount": 10000,
             "currency": "USD", "interval": "month", "enforcement": {"status": "enforcing"}}
    missing = {key: value for key, value in limit.items() if key != "enforcement"}
    alerts = [{"id": "alert_demo", "object": "project.spend_alert", "threshold_amount": 20000,
               "currency": "USD", "interval": "month",
               "notification_channel": {"type": "email", "recipients": ["finance@example.test"]}}]
    fixtures = {
        "/v1/organization/spend_limit": limit,
        "/missing/v1/organization/spend_limit": missing,
        "/v1/organization/projects/proj_demo/spend_alerts": {
            "object": "list", "data": alerts, "first_id": "alert_demo", "last_id": "alert_demo", "has_more": False},
    }
    bodies = {path: json.dumps(value, separators=(",", ":")).encode() for path, value in fixtures.items()}
    write_text(sys.argv[3], json.dumps(fixtures, indent=2) + "\n")
    running = True

    def stop(*_):
        nonlocal running
        running = False

    signal.signal(signal.SIGTERM, stop)
    signal.signal(signal.SIGINT, stop)
    with open(sys.argv[2], "x", encoding="utf-8") as request_log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                path = urlsplit(self.path).path
                if self.headers.get("Authorization") != "Bearer synthetic-demo-admin-key":
                    raise ValueError("Unexpected synthetic fixture authentication")
                body = bodies[path]
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                if self.wfile.write(body) != len(body):
                    raise OSError("Incomplete fixture response write")
                self.wfile.flush()
                entry = json.dumps({"method": "GET", "path": path, "status": 200,
                                    "response_sha256": hashlib.sha256(body).hexdigest()}) + "\n"
                if request_log.write(entry) != len(entry):
                    raise OSError("Incomplete fixture request-log write")
                request_log.flush()

        class Server(http.server.HTTPServer):
            failed = False

            def get_request(self):
                connection, address = super().get_request()
                connection.settimeout(2)
                return connection, address

            def handle_error(self, *_):
                self.failed = True
                print("Synthetic fixture request or write failed.", file=sys.stderr)

        with Server(("127.0.0.1", 0), Fixture) as server:
            server.timeout = 0.05
            address_file = Path(sys.argv[1])
            pending_address = address_file.with_suffix(".tmp")
            write_text(pending_address, f"http://127.0.0.1:{server.server_port}\n")
            os.replace(pending_address, address_file)
            while running and not server.failed:
                server.handle_request()
            if server.failed:
                raise SystemExit(1)


if __name__ == "__main__":
    main()
