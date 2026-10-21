#!/usr/bin/env python3
"""Serve synthetic remote files for the folder-linking terminal replay."""

import http.server
import json
import pathlib
import signal
import sys
import threading
import urllib.parse


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG")
    failures = []
    lock = threading.Lock()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def reject(self):
                with lock:
                    failures.append("unexpected synthetic request")
                body = b'{"error":{"message":"Unexpected synthetic request"}}'
                self.send_response(400)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                try:
                    path = urllib.parse.urlsplit(self.path).path
                    project = self.headers.get("OpenAI-Project", "")
                    if (
                        path != "/after/v1/files"
                        or project != "proj_work"
                        or self.headers.get("Authorization") != "Bearer synthetic-demo-key"
                    ):
                        self.reject()
                        return
                    file_id = "file_remote_" + project
                    body = json.dumps({
                        "object": "list",
                        "data": [{
                            "id": file_id,
                            "object": "file",
                            "filename": "remote-project-notes.txt",
                            "purpose": "user_data",
                            "bytes": 240,
                            "created_at": 1700000000,
                            "status": "processed",
                        }],
                        "has_more": False,
                    }, separators=(",", ":")).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        # Record only these fixed synthetic values. Never log headers.
                        log.write(json.dumps({
                            "method": "GET", "path": path, "project": project,
                            "file_id": file_id, "status": 200,
                        }) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)

            do_POST = reject
            do_PUT = reject
            do_PATCH = reject
            do_DELETE = reject

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        server.daemon_threads = False
        shutdown_threads = []

        def stop(*_):
            if shutdown_threads:
                return
            # shutdown() must run outside the serve_forever() thread.
            worker = threading.Thread(target=server.shutdown)
            shutdown_threads.append(worker)
            worker.start()

        signal.signal(signal.SIGTERM, stop)
        signal.signal(signal.SIGINT, stop)
        try:
            pathlib.Path(sys.argv[1]).write_text(
                f"http://127.0.0.1:{server.server_port}\n", encoding="utf-8"
            )
            server.serve_forever(poll_interval=0.05)
        finally:
            server.server_close()
            for worker in shutdown_threads:
                worker.join()
        if failures:
            raise RuntimeError("synthetic fixture failed: " + ", ".join(failures))


if __name__ == "__main__":
    main()
