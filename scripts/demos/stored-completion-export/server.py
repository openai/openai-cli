#!/usr/bin/env python3
"""Serve synthetic stored completions on loopback, with checked pagination."""

import hashlib
import http.server
import json
import pathlib
import signal
import sys
import threading
import urllib.parse


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_FILE")
    records = [
        {
            "id": f"chatcmpl_demo_{index}",
            "object": "chat.completion",
            "created": 1700000000 + index,
            "model": "model-demo",
            "choices": [{"index": 0, "message": {
                "role": "assistant", "content": f"Synthetic answer {index}."
            }, "finish_reason": "stop"}],
            "metadata": {"purpose": "synthetic-demo", "case": str(index)},
            "future": {"exact_integer": 9007199254740993},
        }
        for index in range(1, 4)
    ]
    encoded = [json.dumps(record, separators=(",", ":")) for record in records]
    fixture = {"records": records, "jsonl": "\n".join(encoded) + "\n"}
    pathlib.Path(sys.argv[3]).write_text(json.dumps(fixture, indent=2) + "\n", encoding="utf-8")
    failures, counts = [], {}
    stop = threading.Event()
    lock = threading.Lock()

    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def send_error(self, code, message=None, explain=None):
                with lock:
                    failures.append(f"unexpected HTTP error {code}")
                super().send_error(code, message, explain)

            def do_GET(self):
                try:
                    parsed = urllib.parse.urlsplit(self.path)
                    scene = parsed.path.split("/")[1]
                    if scene not in {"before", "after", "empty"}:
                        raise ValueError("unexpected scene or request from the existing-file scene")
                    if parsed.path != f"/{scene}/v1/chat/completions":
                        raise ValueError("unexpected request path")
                    if self.headers.get("Authorization") != "Bearer synthetic-demo-key":
                        raise ValueError("request did not use the synthetic credential")
                    if self.headers.get("Content-Length") not in {None, "0"}:
                        raise ValueError("list request unexpectedly has a body")
                    with lock:
                        page = counts.get(scene, 0) + 1
                        counts[scene] = page
                    expected_query = {"limit": ["2"]}
                    if page == 2 and scene != "empty":
                        expected_query["after"] = [records[1]["id"]]
                    if urllib.parse.parse_qs(parsed.query, keep_blank_values=True) != expected_query:
                        raise ValueError("unexpected query or pagination cursor")
                    if page > (1 if scene == "empty" else 2):
                        raise ValueError("unexpected extra page")
                    data = [] if scene == "empty" else records[:2] if page == 1 else records[2:]
                    response = {"object": "list", "data": data,
                                "has_more": scene != "empty" and page == 1,
                                "last_id": data[-1]["id"] if data else None}
                    body = json.dumps(response, separators=(",", ":")).encode()
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({"scene": scene, "page": page, "records": len(data),
                                              "status": 200, "path": parsed.path,
                                              "response_sha256": hashlib.sha256(body).hexdigest()}) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)
                    self.close_connection = True

        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
        server.daemon_threads = False
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            pathlib.Path(sys.argv[1]).write_text(f"http://127.0.0.1:{server.server_port}\n", encoding="utf-8")
            if not stop.wait(120):
                raise TimeoutError("demo fixture exceeded its two-minute lifetime")
        finally:
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic fixture failed: " + ", ".join(failures))


if __name__ == "__main__":
    main()
