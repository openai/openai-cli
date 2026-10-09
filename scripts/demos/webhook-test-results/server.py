#!/usr/bin/env python3
"""Serve synthetic test results without contacting a webhook receiver."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    stop = threading.Event()
    failures = []
    lock = threading.Lock()
    scenes = {"before": 500, "after": 500, "accepted": 200}
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                self.connection.settimeout(5)
                try:
                    scene = self.path.split("/")[1]
                    if scene not in scenes or self.path != f"/{scene}/v1/webhook_endpoints/wh_demo/test":
                        raise ValueError("unexpected request path")
                    size = int(self.headers.get("Content-Length", "0"))
                    if not 0 < size <= 256:
                        raise ValueError("unexpected fixture request length")
                    if json.loads(self.rfile.read(size)) != {"event_type": "response.completed"}:
                        raise ValueError("unexpected event")
                    if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                        raise ValueError("unexpected fixture credentials")
                    result = {"object": "webhook_endpoint.test", "webhook_endpoint_id": "wh_demo",
                              "event_type": "response.completed", "status_code": scenes[scene], "success": True}
                    body = json.dumps(result, separators=(",", ":")).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({"scene": scene, "api_status": 200, "receiver_status": scenes[scene],
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
