#!/usr/bin/env python3
"""Serve synthetic webhook workflows without contacting a receiver."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading

CATALOG = {"object": "list", "data": ["response.completed", "batch.completed", "response.failed", "future.demo_event"]}
SETTINGS = {"name": "Response notifications", "url": "https://example.com/webhook",
            "event_types": ["response.completed", "response.failed"]}


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG CONFIRMATION_FILE")
    confirmation = pathlib.Path(sys.argv[3])
    stop = threading.Event()
    failures = []
    requests = []
    lock = threading.Lock()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                self.respond()

            def do_POST(self):
                self.respond()

            def respond(self):
                self.connection.settimeout(5)
                try:
                    scene = self.path.split("/")[1]
                    prefix = f"/{scene}/v1"
                    route = self.command + " " + self.path.removeprefix(prefix)
                    if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                        raise ValueError("unexpected fixture credentials")
                    data = None
                    if self.command == "POST":
                        size = int(self.headers.get("Content-Length", "0"))
                        if not 0 < size <= 1024:
                            raise ValueError("unexpected fixture request length")
                        data = json.loads(self.rfile.read(size))
                    if scene in {"before", "after"} and route == "POST /webhook_endpoints/wh_demo/test":
                        if data != {"event_type": "response.completed"}:
                            raise ValueError("unexpected test event")
                        result = {"object": "webhook_endpoint.test", "webhook_endpoint_id": "wh_demo",
                                  "event_type": "response.completed", "status_code": 500, "success": True}
                        operation = "test"
                    elif scene in {"discovery", "guided"} and route == "GET /webhook_event_types":
                        result, operation = CATALOG, "catalog"
                    elif scene == "guided" and route == "POST /webhook_endpoints":
                        if not confirmation.exists() or data != SETTINGS:
                            raise ValueError("creation preceded confirmation or changed settings")
                        result = {"object": "webhook_endpoint", "id": "whe_demo", **SETTINGS,
                                  "signing_secret": "whsec_fake_for_demo_only"}
                        operation = "create"
                    else:
                        raise ValueError("unexpected request route")
                    with lock:
                        if len(requests) >= 8 or operation == "create" and "create" in requests:
                            raise ValueError("duplicate or excessive fixture requests")
                        requests.append(operation)
                    body = json.dumps(result, separators=(",", ":")).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({"scene": scene, "operation": operation, "api_status": 200,
                                              "response_sha256": hashlib.sha256(body).hexdigest()}) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)
                    self.close_connection = True

            def send_error(self, code, message=None, explain=None):
                with lock:
                    failures.append("unexpected HTTP method")
                super().send_error(code, message, explain)

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        server.daemon_threads = False
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}\n")
            stop.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic fixture failed: " + ", ".join(failures))


if __name__ == "__main__":
    main()
