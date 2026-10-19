#!/usr/bin/env python3
"""Stateful synthetic limits API. Bind only to loopback; never log credentials."""

import copy
import hashlib
import http.server
import json
from pathlib import Path
import signal
import socket
import sys
import threading
import time
from urllib.parse import urlsplit, parse_qs


SCENES = ("org-limit", "org-limit-delete", "project-limit", "alert-create", "alert-edit",
          "alert-delete", "rate-update", "uncertain", "slow-cancel")


def write_text(path, value):
    with Path(path).open("x", encoding="utf-8") as destination:
        if destination.write(value) != len(value):
            raise OSError("incomplete fixture evidence write")


def initial_state(project):
    scope = "project" if project else "organization"
    return {
        "limit": {"object": scope + ".spend_limit", "threshold_amount": 10000,
                  "currency": "USD", "interval": "month", "enforcement": {"status": "enforcing"}},
        "alerts": [{"object": scope + ".spend_alert", "id": "alert_demo", "threshold_amount": 10000,
                    "currency": "USD", "interval": "month", "notification_channel": {
                        "type": "email", "recipients": ["finance@example.test"], "subject_prefix": "Demo"}}],
        "rates": [{"object": "project.rate_limit", "id": "rl_demo", "model": "synthetic-model",
                   "max_requests_per_1_minute": 100, "max_tokens_per_1_minute": 20000,
                   "max_images_per_1_minute": 5, "max_requests_per_1_day": 1000,
                   "max_audio_megabytes_per_1_minute": 10, "batch_1_day_max_input_tokens": 50000}],
    }


def page(records):
    return {"object": "list", "data": records, "has_more": False,
            "first_id": records[0]["id"] if records else None,
            "last_id": records[-1]["id"] if records else None}


def main():
    if len(sys.argv) != 5:
        raise SystemExit("usage: server.py ADDRESS_FILE REQUEST_LOG FIXTURE_METADATA GATE_DIRECTORY")
    gate = Path(sys.argv[4])
    gate.mkdir()
    states = {name: initial_state(name in {"project-limit", "alert-edit", "rate-update"}) for name in SCENES}
    write_text(sys.argv[3], json.dumps({"data": "synthetic only", "initial": states}, indent=2) + "\n")
    stop, lock, failures = threading.Event(), threading.Lock(), []
    with open(sys.argv[2], "x", encoding="utf-8") as log:
        def record(entry):
            line = json.dumps(entry, separators=(",", ":")) + "\n"
            with lock:
                if log.write(line) != len(line):
                    raise OSError("incomplete request log write")
                log.flush()

        class Fixture(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def handle_request(self):
                if self.headers.get("Authorization") != "Bearer synthetic-demo-admin-key":
                    raise AssertionError("unexpected fixture authentication")
                parts = urlsplit(self.path).path.strip("/").split("/")
                if len(parts) < 4 or parts[0] not in states or parts[1] != "v1" or parts[2] != "organization":
                    raise AssertionError("unexpected fixture route")
                scene, project, tail = parts[0], False, parts[3:]
                if tail[:1] == ["projects"]:
                    if tail[1:2] != ["proj_demo"]:
                        raise AssertionError("unexpected synthetic project")
                    project, tail = True, tail[2:]
                state, scope = states[scene], "project" if project else "organization"
                payload = None
                if self.command == "POST":
                    payload = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                status, result = 200, None
                if self.command == "GET" and tail == ["costs"]:
                    query = parse_qs(urlsplit(self.path).query)
                    expected_project = ["proj_demo"] if scene in {"project-limit", "alert-edit", "rate-update"} else []
                    if query.get("project_ids[]", []) != expected_project:
                        raise AssertionError("Costs scope differs from the selected workspace")
                    start = int(query["start_time"][0])
                    result = {"object": "page", "data": [{"object": "bucket", "start_time": start,
                              "end_time": start + 86400, "results": [{"object": "organization.costs.result",
                              "amount": {"value": 37.25, "currency": "usd"},
                              "project_id": "proj_demo" if scene in {"project-limit", "alert-edit", "rate-update"} else None,
                              "line_item": None}]}], "has_more": False, "next_page": None}
                elif self.command == "GET" and not tail and project:
                    result = {"id": "proj_demo", "object": "organization.project", "name": "Synthetic project", "status": "active"}
                elif tail == ["spend_limit"]:
                    if self.command == "GET":
                        if scene == "slow-cancel":
                            record({"scene": scene, "method": "GET", "resource": "spend_limit", "status": "held"})
                            write_text(gate / "slow-cancel.requested", "synthetic request received\n")
                            deadline = time.monotonic() + 30
                            while not (gate / "slow-cancel.release").exists():
                                if stop.wait(0.05):
                                    return
                                if time.monotonic() >= deadline:
                                    raise TimeoutError("slow scene did not confirm cancellation")
                            return
                        result = state["limit"]
                        if result is None:
                            status, result = 404, {"error": {"message": "Synthetic limit is absent"}}
                    elif self.command == "POST":
                        if set(payload) != {"threshold_amount", "currency", "interval"} or payload["currency"] != "USD" or payload["interval"] != "month":
                            raise AssertionError("unexpected limit mutation body")
                        state["limit"] = dict(payload, object=scope + ".spend_limit", enforcement={"status": "enforcing"})
                        result = state["limit"]
                    elif self.command == "DELETE":
                        state["limit"] = None
                        result = {"object": scope + ".spend_limit.deleted", "deleted": True}
                elif tail[:1] == ["spend_alerts"]:
                    alert_id = tail[1] if len(tail) == 2 else None
                    alert = next((value for value in state["alerts"] if value["id"] == alert_id), None)
                    if self.command == "GET":
                        result = page(state["alerts"]) if alert_id is None else alert
                    elif self.command == "POST":
                        if set(payload) != {"threshold_amount", "currency", "interval", "notification_channel"} or payload["currency"] != "USD" or payload["interval"] != "month":
                            raise AssertionError("unexpected alert mutation body")
                        result = dict(payload, object=scope + ".spend_alert", id=alert_id or "alert_created")
                        if alert_id:
                            if alert is None:
                                raise AssertionError("edit targeted an unknown synthetic alert")
                            state["alerts"][state["alerts"].index(alert)] = result
                        else:
                            state["alerts"].append(result)
                    elif self.command == "DELETE" and alert is not None:
                        state["alerts"].remove(alert)
                        result = {"object": scope + ".spend_alert.deleted", "id": alert_id, "deleted": True}
                elif tail[:1] == ["rate_limits"] and project:
                    if self.command == "GET":
                        result = page(state["rates"])
                    elif self.command == "POST" and tail[1:] == ["rl_demo"]:
                        if payload != {"max_requests_per_1_minute": 250}:
                            raise AssertionError("rate update must change only the selected dimension")
                        state["rates"][0].update(payload)
                        result = state["rates"][0]
                if result is None:
                    raise AssertionError("unsupported synthetic request")
                body = json.dumps(copy.deepcopy(result), separators=(",", ":")).encode()
                entry = {"scene": scene, "method": self.command, "path": urlsplit(self.path).path,
                         "status": status, "response_sha256": hashlib.sha256(body).hexdigest()}
                if payload is not None:
                    entry["body"] = payload
                if scene == "uncertain" and self.command == "POST":
                    entry["status"] = "response_lost_after_write"
                    record(entry)
                    self.connection.shutdown(socket.SHUT_RDWR)
                    self.connection.close()
                    self.close_connection = True
                    return
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.send_header("x-should-retry", "false")
                self.end_headers()
                if self.wfile.write(body) != len(body):
                    raise OSError("incomplete fixture response")
                self.wfile.flush()
                record(entry)

            do_GET = handle_request
            do_POST = handle_request
            do_DELETE = handle_request

        class Server(http.server.ThreadingHTTPServer):
            daemon_threads = False

            def handle_error(self, *_):
                with lock:
                    failures.append("fixture handler failed")

        server = Server(("127.0.0.1", 0), Fixture)
        signal.signal(signal.SIGTERM, lambda *_: stop.set())
        signal.signal(signal.SIGINT, lambda *_: stop.set())
        worker = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.05})
        worker.start()
        try:
            write_text(sys.argv[1], f"http://127.0.0.1:{server.server_port}\n")
            stop.wait()
        finally:
            stop.set()
            server.shutdown()
            server.server_close()
            worker.join()
        if failures:
            raise RuntimeError("synthetic fixture failed; evidence is incomplete")


if __name__ == "__main__":
    main()
