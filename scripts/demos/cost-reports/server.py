#!/usr/bin/env python3
"""Serve two synthetic Costs pages and record requests without credentials."""

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
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_METADATA")

    def result(project, value):
        return {
            "object": "organization.costs.result",
            "project_id": project,
            "amount": {"value": value, "currency": "usd"},
        }

    def page(results, more, cursor):
        return json.dumps({
            "object": "page",
            "data": [{
                "object": "bucket", "start_time": 1790812800,
                "end_time": 1790899200, "results": results,
            }],
            "has_more": more, "next_page": cursor,
        }, separators=(",", ":")).encode()

    pages = {
        "": page([result("proj_beta", 4.56), result("proj_alpha", 10)], True, "page-two"),
        "page-two": page([result("proj_alpha", 2.34)], False, None),
    }
    pathlib.Path(sys.argv[3]).write_text(json.dumps({
        "data": "synthetic only",
        "expected_totals": {"proj_alpha": "12.34", "proj_beta": "4.56"},
        "currency": "usd",
        "pages": {cursor: {"bytes": len(body), "sha256": hashlib.sha256(body).hexdigest()}
                  for cursor, body in pages.items()},
    }, indent=2) + "\n", encoding="utf-8")

    failures = []
    lock = threading.Lock()
    stop = threading.Event()
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        class Fixture(http.server.BaseHTTPRequestHandler):
            def setup(self):
                super().setup()
                self.connection.settimeout(5)

            def log_message(self, *_):
                pass

            def do_GET(self):
                try:
                    request = urllib.parse.urlsplit(self.path)
                    query = urllib.parse.parse_qs(request.query, keep_blank_values=True)
                    assert request.path in {
                        "/before/organization/costs", "/after/organization/costs",
                    }, "unexpected synthetic request path"
                    assert self.headers.get("Authorization") == "Bearer synthetic-cost-report-demo-admin", \
                        "the request must select the synthetic Admin credential"
                    expected = {
                        "start_time": ["1790812800"], "end_time": ["1791417600"],
                        "bucket_width": ["1d"], "group_by[]": ["project_id"], "limit": ["180"],
                    }
                    cursor = query.pop("page", [""])
                    assert len(cursor) == 1 and cursor[0] in pages, "unexpected pagination cursor"
                    assert query == expected, "unexpected Costs query parameters"
                    body = pages[cursor[0]]
                    self.send_response(200)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    self.wfile.write(body)
                    self.wfile.flush()
                    with lock:
                        log.write(json.dumps({
                            "path": request.path, "page": cursor[0], "status": 200,
                            "response_sha256": hashlib.sha256(body).hexdigest(),
                        }) + "\n")
                        log.flush()
                except Exception as error:
                    with lock:
                        failures.append(type(error).__name__)

        server = http.server.HTTPServer(("127.0.0.1", 0), Fixture)
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.02})
        worker.start()
        try:
            pathlib.Path(sys.argv[1]).write_text(
                f"http://127.0.0.1:{server.server_port}\n", encoding="utf-8")
            stop.wait()
        finally:
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic request, response, or log failure: " + ", ".join(failures))


if __name__ == "__main__":
    main()
