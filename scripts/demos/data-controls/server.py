#!/usr/bin/env python3
"""Serve deterministic retention and storage responses on loopback only."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading


RETENTION = {"object": "project.data_retention", "type": "organization_default"}
STORAGE = {
    "object": "organization.external_storage",
    "id": "ext_returned",
    "project_id": "proj_demo",
    "provider": {
        "type": "aws", "account_id": "000000000000", "bucket": "synthetic-demo-bucket",
        "external_id": "synthetic-external-id", "region": "us-east-1",
        "role_arn": "arn:aws:iam::000000000000:role/synthetic-demo",
    },
    "geography": "us", "status": "pending", "created_at": 1,
}
RESPONSES = {
    "retention": RETENTION,
    "pending": STORAGE,
    "validated": {**STORAGE, "status": "validated"},
}
ROUTES = {
    "retention": ("GET", "/organization/projects/proj_demo/data_retention"),
    "pending": ("POST", "/organization/external_storage/ext_requested/validate"),
    "validated": ("GET", "/organization/external_storage/ext_returned"),
}


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_METADATA")
    bodies = {name: json.dumps(value, separators=(",", ":")).encode() for name, value in RESPONSES.items()}
    metadata = {name: {"response": RESPONSES[name], "sha256": hashlib.sha256(body).hexdigest()}
                for name, body in bodies.items()}
    pathlib.Path(sys.argv[3]).write_text(json.dumps(metadata, indent=2) + "\n")
    failures = []
    lock = threading.Lock()
    stopped = threading.Event()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def setup(self):
                super().setup()
                self.connection.settimeout(3)

            def log_message(self, *_):
                pass

            def send_error(self, code, message=None, explain=None):
                with lock:
                    failures.append("unexpected HTTP error")
                super().send_error(code, message, explain)

            def handle_request(self):
                try:
                    parts = self.path.split("/", 3)
                    if len(parts) != 4 or parts[1] not in {"before", "after"}:
                        raise ValueError("unexpected fixture scene")
                    mode, name, route = parts[1], parts[2], "/" + parts[3]
                    if name not in ROUTES or (self.command, route) != ROUTES[name]:
                        raise ValueError("unexpected fixture method or path")
                    if self.headers.get("Authorization") != "Bearer synthetic-admin-key":
                        raise ValueError("fixture requires synthetic admin credentials")
                    if self.headers.get("Transfer-Encoding") or int(self.headers.get("Content-Length", "0")) != 0:
                        raise ValueError("fixture expects an empty request body")
                    body = bodies[name]
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    if self.wfile.write(body) != len(body):
                        raise OSError("short fixture response write")
                    self.wfile.flush()
                    with lock:
                        entry = json.dumps({"scene": f"{mode}-{name}", "method": self.command,
                                            "path": self.path, "status": 200,
                                            "response_sha256": hashlib.sha256(body).hexdigest()}) + "\n"
                        if log.write(entry) != len(entry):
                            raise OSError("short fixture log write")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)
                    self.close_connection = True

            do_GET = handle_request
            do_POST = handle_request

        class Server(http.server.ThreadingHTTPServer):
            daemon_threads = False

            def handle_error(self, *_):
                with lock:
                    failures.append("request handler failure")

        server = Server(("127.0.0.1", 0), Fixture)
        signal.signal(signal.SIGTERM, lambda *_: stopped.set())
        signal.signal(signal.SIGINT, lambda *_: stopped.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}\n")
            stopped.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join(timeout=5)
        if worker.is_alive() or failures:
            raise RuntimeError("fixture request, output, or shutdown failure")


if __name__ == "__main__":
    main()
