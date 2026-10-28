#!/usr/bin/env python3
"""Exercise vector-store commands in owned PTYs against a synthetic loopback API."""

if not __debug__:
    raise SystemExit("Run without -O or -OO. This checker requires Python assertions.")

import argparse
import codecs
from email import policy
from email.parser import BytesParser
import errno
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import select
import signal
import sys
import tempfile
import threading
import time
from urllib.parse import parse_qs, urlsplit


KEY = "synthetic-vector-workspace-key"
NAME = "Synthetic copper kite"
FILENAME = "fixture source.txt"
TEXT = b"The copper kite has seven blue ribbons.\n"
QUERY = "How many ribbons does the copper kite have?"
CANARY = "SYNTHETIC_PRIVATE_API_MESSAGE"
FILE_ID = "file_synthetic"
EXISTING_ID = "vs_existing"
CREATED_ID = "vs_created"
CASES = ("workflow", "upload-failure", "attachment-failure", "tiny",
         "ctrl-c", "sigterm", "busy-ctrl-c", "busy-sigterm",
         "management", "management-failure", "search", "search-narrow")
INTERRUPTED = threading.Event()


class HarnessInterrupted(Exception):
    pass


def load_harness(root):
    spec = importlib.util.spec_from_file_location(
        "vector_workspace_terminal", root / "scripts/image_picker_harness.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def store(store_id, name):
    return {"id": store_id, "object": "vector_store", "name": name,
            "created_at": 1700000000, "usage_bytes": 0, "status": "completed",
            "file_counts": {"in_progress": 0, "completed": 0, "failed": 0,
                            "cancelled": 0, "total": 0}, "metadata": {}}


def attachment(store_id, status):
    return {"id": FILE_ID, "object": "vector_store.file", "vector_store_id": store_id,
            "created_at": 1700000000, "status": status, "usage_bytes": 100,
            "last_error": None, "attributes": {}}


class Fixture(http.server.HTTPServer):
    """One owned server thread suffices because workspace requests are sequential."""

    def __init__(self, case):
        super().__init__(("127.0.0.1", 0), Handler)
        self.timeout = .1
        self.case = case
        self.records, self.failures = [], []
        self.lock = threading.Lock()
        self.started, self.release, self.stopped = (threading.Event() for _ in range(3))
        self.uploads = self.attachments = 0
        self.created = self.attached = False
        self.attached_store = None
        self.store_changes = {}
        self.batch = None
        self.attributes = {}

    @property
    def endpoint(self):
        return f"http://127.0.0.1:{self.server_port}/v1"

    def serve(self):
        while not self.stopped.is_set():
            self.handle_request()

    def record(self, method, path, body, status):
        # Record only validated synthetic fields. Never record request headers.
        with self.lock:
            self.records.append({"method": method, "path": path, "body": body,
                                 "response_status": status})
        self.started.set()

    def count(self, method, path):
        with self.lock:
            return sum(row["method"] == method and row["path"] == path for row in self.records)


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def setup(self):
        self.request.settimeout(2)
        super().setup()

    def respond(self, status, body):
        payload = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("X-Request-ID", "req_synthetic_vector_workspace")
        self.end_headers()
        try:
            self.wfile.write(payload)
        except (BrokenPipeError, ConnectionResetError):
            # Cancellation deliberately disconnects before the gated response.
            pass

    def read_body(self):
        # This bound applies only to the fixture's known 40-byte input.
        length = int(self.headers.get("Content-Length", "0"))
        assert not self.headers.get("Transfer-Encoding"), "fixture expects known-length multipart"
        assert 0 < length <= 1024 * 1024, "unexpected synthetic request size"
        body = self.rfile.read(length)
        assert len(body) == length, "incomplete synthetic request"
        return body

    def upload_body(self):
        content_type = self.headers.get("Content-Type", "")
        assert content_type.startswith("multipart/form-data;"), "expected multipart upload"
        message = BytesParser(policy=policy.default).parsebytes(
            b"Content-Type: " + content_type.encode("ascii") + b"\r\n\r\n" + self.read_body())
        assert message.is_multipart(), "multipart input did not parse"
        parts = {}
        for part in message.iter_parts():
            name = part.get_param("name", header="content-disposition")
            assert name not in parts, "duplicate multipart field"
            parts[name] = part
        assert set(parts) == {"file", "purpose"}, "unexpected multipart fields"
        assert parts["file"].get_filename() == FILENAME, "unexpected synthetic filename"
        assert parts["file"].get_payload(decode=True) == TEXT, "unexpected synthetic file content"
        assert parts["purpose"].get_payload(decode=True) == b"assistants", "unexpected file purpose"
        return {"filename": FILENAME, "purpose": "assistants", "bytes": len(TEXT),
                "sha256": hashlib.sha256(TEXT).hexdigest()}

    def management_response(self, path, method, url):
        if not self.server.case.startswith("management"):
            return None
        server = self.server
        base = f"/v1/vector_stores/{EXISTING_ID}"
        batch_path = base + "/file_batches/vsfb_synthetic"
        if path == base and method == "POST":
            body = json.loads(self.read_body())
            assert len(body) == 1 and set(body) <= {"name", "metadata", "expires_after"}
            if server.case == "management-failure":
                return 500, None, body
            server.store_changes.update(body)
            return 200, {**store(EXISTING_ID, "Existing synthetic store"), **server.store_changes}, body
        if path == base and method == "GET":
            return 200, {**store(EXISTING_ID, "Existing synthetic store"), **server.store_changes}, None
        if path == base and method == "DELETE":
            return 200, {"id": EXISTING_ID, "deleted": True}, None
        if path == "/v1/files" and method == "GET":
            assert parse_qs(url.query) == {"purpose": ["assistants"], "limit": ["20"], "order": ["desc"]}
            return 200, {"data": [{"id": FILE_ID, "filename": FILENAME},
                                  {"id": "file_second", "filename": "second synthetic.txt"}], "has_more": False}, None
        if path == base + "/file_batches" and method == "POST":
            body = json.loads(self.read_body())
            assert body == {"file_ids": [FILE_ID, "file_second"], "attributes": {"category": "notes", "revision": 7, "enabled": True},
                            "chunking_strategy": {"type": "static", "static": {"max_chunk_size_tokens": 800, "chunk_overlap_tokens": 200}}}
            server.attributes = body["attributes"]
            server.attached, server.attached_store = True, EXISTING_ID
            server.batch = {"id": "vsfb_synthetic", "vector_store_id": EXISTING_ID, "status": "in_progress",
                            "file_counts": {"total": 2, "completed": 1, "in_progress": 1, "failed": 0, "cancelled": 0}}
            return 200, server.batch, body
        if path == batch_path and method == "GET":
            assert server.batch is not None
            return 200, server.batch, None
        if path == batch_path + "/cancel" and method == "POST":
            assert server.batch is not None
            server.batch = {**server.batch, "status": "cancelled", "file_counts": {
                "total": 2, "completed": 1, "in_progress": 0, "failed": 0, "cancelled": 1}}
            return 200, server.batch, None
        if path == batch_path + "/files" and method == "GET":
            assert parse_qs(url.query) == {"limit": ["20"]}
            return 200, {"data": [attachment(EXISTING_ID, "completed"),
                                 {**attachment(EXISTING_ID, "in_progress"), "id": "file_second"}], "has_more": False}, None
        if path == base + f"/files/{FILE_ID}/content" and method == "GET":
            return 200, {"object": "vector_store.file_content.page", "has_more": False, "next_page": None,
                         "data": [{"type": "text", "text": TEXT.decode().strip()},
                                  {"type": "text", "text": "Final indexed passage."}]}, None
        if path == base + f"/files/{FILE_ID}":
            if method == "GET":
                return 200, {**attachment(EXISTING_ID, "completed"), "attributes": server.attributes}, None
            if method == "POST":
                body = json.loads(self.read_body())
                assert body == {"attributes": {"reviewed": True, "revision": 8}}
                server.attributes = body["attributes"]
                return 200, {**attachment(EXISTING_ID, "completed"), "attributes": server.attributes}, body
            if method == "DELETE":
                return 200, {"id": FILE_ID, "deleted": True}, None
        return None

    def dispatch(self):
        assert self.headers.get("Authorization") == "Bearer " + KEY, "expected isolated fake credential"
        assert self.headers.get("OpenAI-Project") == "proj_synthetic", "project setting was not preserved"
        assert self.headers.get("OpenAI-Organization") == "org_synthetic", "organization setting was not preserved"
        assert self.headers.get("X-Workspace-Fixture") == "synthetic", "request header was not preserved"
        url = urlsplit(self.path)
        path, method = url.path, self.command
        server = self.server
        status, request_body = 200, None
        managed = self.management_response(path, method, url)
        if managed is not None:
            status, response, request_body = managed
        elif method == "GET" and path == "/v1/vector_stores":
            assert parse_qs(url.query) == {"limit": ["20"], "order": ["desc"]}, "unexpected store list options"
            stores = [store(EXISTING_ID, "Existing synthetic store")]
            if server.created:
                stores.insert(0, store(CREATED_ID, NAME))
            response = {"object": "list", "data": stores, "has_more": False}
        elif method == "POST" and path == "/v1/vector_stores":
            request_body = json.loads(self.read_body())
            assert request_body == {"name": NAME}, "unexpected store creation input"
            server.created = True
            response = store(CREATED_ID, NAME)
        elif method == "GET" and path in (f"/v1/vector_stores/{EXISTING_ID}", f"/v1/vector_stores/{CREATED_ID}"):
            store_id = path.rsplit("/", 1)[-1]
            response = store(store_id, NAME if store_id == CREATED_ID else "Existing synthetic store")
        elif method == "POST" and path == "/v1/files":
            request_body = self.upload_body()
            server.uploads += 1
            status = 400 if server.case == "upload-failure" and server.uploads == 1 else 200
            response = {"id": FILE_ID, "object": "file", "created_at": 1700000000,
                        "purpose": "assistants", "filename": FILENAME, "bytes": len(TEXT), "status": "processed"}
        elif re.fullmatch(r"/v1/vector_stores/vs_(?:existing|created)/files", path):
            store_id = path.split("/")[3]
            if method == "POST":
                request_body = json.loads(self.read_body())
                assert request_body == {"file_id": FILE_ID}, "unexpected attachment input"
                server.attachments += 1
                status = 400 if server.case == "attachment-failure" and server.attachments == 1 else 200
                server.attached = status == 200
                server.attached_store = store_id
                response = attachment(store_id, "in_progress")
            else:
                assert method == "GET", "unsupported fixture method"
                assert parse_qs(url.query) == {"limit": ["20"], "order": ["desc"]}, "unexpected file list options"
                files = [attachment(store_id, "completed")] if server.attached else []
                response = {"object": "list", "data": files, "has_more": False}
        elif method == "GET" and re.fullmatch(
                r"/v1/vector_stores/vs_(?:existing|created)/files/file_synthetic", path):
            store_id = path.split("/")[3]
            assert server.attached_store == store_id, "attachment lookup used an unexpected store"
            if server.case == "attachment-failure" and server.attachments == 1 and not server.attached:
                # A manual retry verifies remote state before another attachment POST.
                status, response = 404, None
            else:
                assert server.attached, "poll preceded attachment"
                response = attachment(store_id, "completed")
        elif method == "POST" and re.fullmatch(r"/v1/vector_stores/vs_(?:existing|created)/search", path):
            request_body = json.loads(self.read_body())
            query = request_body["query"]
            allowed = ("kite", "kite absent") if server.case.startswith("search") else (QUERY,)
            assert query in allowed and request_body == {"query": query, "max_num_results": 10}, "unexpected search input"
            assert server.case.startswith("search") or server.attached, "search preceded indexing"
            matches = [{"file_id": FILE_ID, "filename": FILENAME, "score": .99, "attributes": {},
                        "content": [{"type": "text", "text": TEXT.decode().strip()}]}]
            if query == "kite":
                matches.append({"file_id": "file_second", "filename": "second source.txt", "score": .75,
                                "content": [{"type": "text", "text": "Another kite flies at dusk."}]})
            response = {"object": "vector_store.search_results.page", "search_query": [query],
                        "has_more": False, "next_page": None, "data": [] if query == "kite absent" else matches}
        else:
            raise AssertionError("request escaped the fixture endpoint contract")
        server.record(method, path, request_body, status)
        if server.case.startswith("busy-") and path == "/v1/vector_stores":
            # Cleanup releases this gate after the command exits.
            assert server.release.wait(30), "busy fixture was not released"
        if status != 200:
            response = {"error": {"message": CANARY, "type": "invalid_request_error",
                                  "code": "invalid_value", "param": None}}
        self.respond(status, response)

    def handle_request(self):
        try:
            self.dispatch()
        except (AssertionError, ValueError, OSError) as error:
            # Error text is authored locally and excludes incoming request values.
            with self.server.lock:
                self.server.failures.append(type(error).__name__ + ": " + str(error))
            self.respond(400, {"error": {"message": "Synthetic fixture rejected the request.",
                                       "type": "invalid_request_error"}})

    do_GET = do_POST = do_DELETE = handle_request


def terminal_class(harness, theme="mono"):
    class Terminal(harness.Terminal):
        def __init__(self, *args, **kwargs):
            super().__init__(*args, **kwargs)
            self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
            self.queries = b""

        def read(self, duration=.05):
            if INTERRUPTED.is_set():
                raise HarnessInterrupted("The harness received a stop signal.")
            if not select.select([self.master], [], [], duration)[0]:
                return
            try:
                fresh = os.read(self.master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    return
                raise
            if not fresh:
                return
            self.raw.extend(fresh)
            decoded = self.decoder.decode(fresh)
            if decoded:
                self.events.append([time.monotonic() - self.started, "o", decoded])
            pending = self.queries + fresh
            pattern = rb"\x1b\[(?:6n|0?c|\?7\$p)|\x1b\]11;\?(?:\x07|\x1b\\)"
            while match := re.search(pattern, pending):
                query = match.group()
                if query.startswith(b"\x1b]11"):
                    reply = b"\x1b]11;rgb:ffff/ffff/ffff\x1b\\" if theme == "light" else b"\x1b]11;rgb:1818/1818/1818\x1b\\"
                elif query == b"\x1b[6n":
                    reply = b"\x1b[1;1R"
                elif query == b"\x1b[?7$p":
                    reply = b"\x1b[?7;1$y"
                else:
                    reply = b"\x1b[?1;2c"
                self.send(reply)
                pending = pending[match.end():]
            self.queries = pending[-128:]

        def save(self, destination):
            destination.with_suffix(".raw").write_bytes(self.raw)
            header = {"version": 2, "width": self.initial_size[0], "height": self.initial_size[1],
                      "title": "Vector-store workspace | synthetic loopback API",
                      "env": {"TERM": "xterm-256color"}}
            destination.with_suffix(".cast").write_text(
                "\n".join(json.dumps(row) for row in [header, *self.events]) + "\n")

    return Terminal


def drain(terminal, duration=.15):
    deadline = time.monotonic() + duration
    while time.monotonic() < deadline:
        terminal.read(min(.03, max(0, deadline - time.monotonic())))


def action(terminal, keys, marker):
    mark = len(terminal.raw)
    terminal.send(keys)
    terminal.wait(marker, after=mark)


def paste(terminal, value):
    terminal.send(b"\x1b[200~" + value.encode() + b"\x1b[201~")
    drain(terminal)


def open_upload(terminal):
    action(terminal, b"\x1b[B\x1b[C\r", "Local file path")


def upload_path(terminal, path):
    open_upload(terminal)
    paste(terminal, str(path))
    # Enter moves from the path input to Upload. The next Enter submits.
    terminal.send(b"\r\r\r\r")


def quit_normally(terminal):
    action(terminal, b"\x1b", "Find store by name or ID")
    terminal.send(b"q")
    terminal.finish(0)


def replace_editor(terminal, text):
    terminal.send(b"\x01\x0b")  # Beginning of line, then delete through the end.
    paste(terminal, text)


def submit_editor(terminal, text, marker):
    replace_editor(terminal, text)
    action(terminal, b"\r\r\r\r", marker)


def search_scenario(terminal, server, harness):
    path = f"/v1/vector_stores/{EXISTING_ID}/search"
    terminal.send(b"kite")
    drain(terminal)
    assert server.count("POST", path) == 0, "typing submitted a search"
    mark = len(terminal.raw)
    action(terminal, b"\r", FILENAME)
    terminal.wait("The copper kite", after=mark)
    terminal.wait("ribbons.", after=mark)
    assert server.count("POST", path) == 1, "one Enter should send one search"

    action(terminal, harness.DOWN, "Another kite flies at dusk.")
    action(terminal, b"\r", "Relevance score: 0.750")
    mark = len(terminal.raw)
    action(terminal, b"\x1b", "Esc edit query")
    terminal.wait("Another kite flies at dusk.", after=mark)
    action(terminal, b"\x1b", "Enter search")
    terminal.send(b" absent")
    drain(terminal)
    assert server.count("POST", path) == 1, "navigation or query editing submitted a search"
    action(terminal, b"\r", "No matches returned.")
    assert server.count("POST", path) == 2, "query refinement did not submit exactly once"

    # Returning a match proves both the empty state and the active query are editable.
    replace_editor(terminal, "kite")
    action(terminal, b"\r", FILENAME)
    assert server.count("POST", path) == 3, "empty-result retry did not submit exactly once"
    action(terminal, b"\r", "Relevance score: 0.990")
    action(terminal, b"\x1b", "Esc edit query")
    action(terminal, b"\x1b", "Enter search")
    quit_normally(terminal)
    assert [row["body"]["query"] for row in server.records if row["path"] == path] == ["kite", "kite absent", "kite"]


def management_scenario(name, terminal, server, harness):
    action(terminal, b"\t" * 5 + b"\r", "Store details")
    action(terminal, harness.DOWN + b"\r", "Store name")
    submit_editor(terminal, "Renamed synthetic store", "HTTP 500" if name.endswith("failure") else "Store updated.")
    update_path = f"/v1/vector_stores/{EXISTING_ID}"
    assert server.count("POST", update_path) == 1, "rename submitted more than once"
    if name.endswith("failure"):
        assert not server.store_changes, "failed mutation replaced retained store state"
        action(terminal, b"\x1b", "Store details")
        action(terminal, b"\r", "Estimated usage this month")
        terminal.wait("Existing synthetic store")
        action(terminal, b"\x1b", "Store details")
        action(terminal, b"\x1b", "Search your files")
        action(terminal, b"\x1b", "Find store by name or ID")
        terminal.send(b"q")
        terminal.finish(1)
        return
    harness.wait_submit_guard(terminal)
    action(terminal, harness.DOWN * 2 + b"\r", "Days since last activity")
    submit_editor(terminal, "7", "Store updated.")
    harness.wait_submit_guard(terminal)
    action(terminal, harness.DOWN * 3 + b"\r", "Metadata (JSON")
    submit_editor(terminal, '{"category":"synthetic"}', "Store updated.")
    harness.wait_submit_guard(terminal)
    action(terminal, b"\r", "0 bytes")
    terminal.wait("7 days since last activity")
    action(terminal, b"\x1b", "Store details")
    action(terminal, harness.DOWN * 4 + b"\r", "second synthetic.txt")
    action(terminal, b"\t" * 3 + b"\r", "Attachment options")
    submit_editor(terminal, '{"attributes":{"category":"notes","revision":7,"enabled":true},"chunking_strategy":{"type":"static","static":{"max_chunk_size_tokens":800,"chunk_overlap_tokens":200}}}', "Attachment options saved.")
    terminal.send(b"\r" + harness.DOWN + b"\r")
    action(terminal, b"\t\r\r\r", "Batch status: in_progress")
    assert server.count("POST", update_path + "/file_batches") == 1
    harness.wait_submit_guard(terminal)
    action(terminal, harness.DOWN + b"\r", "completed")
    action(terminal, b"\r", "File details")
    action(terminal, harness.DOWN + b"\r", "Final indexed passage.")
    terminal.wait(TEXT.decode().strip())
    action(terminal, b"\x1b", "File details")
    action(terminal, harness.DOWN * 2 + b"\r", "Attributes (JSON")
    submit_editor(terminal, '{"reviewed":true,"revision":8}', "File attributes updated.")
    harness.wait_submit_guard(terminal)
    action(terminal, harness.DOWN * 3 + b"\r", "Confirm file ID")
    submit_editor(terminal, "wrong", "The ID does not match.")
    assert server.count("DELETE", update_path + f"/files/{FILE_ID}") == 0
    # A failed confirmation retains action focus. Return to the editor before replacing it.
    terminal.send(b"\t\t")
    submit_editor(terminal, FILE_ID, "Attachment removed.")
    harness.wait_submit_guard(terminal)
    action(terminal, b"\x1b", "Batch details")
    action(terminal, harness.DOWN * 3 + b"\r", "Confirm batch ID")
    submit_editor(terminal, "vsfb_synthetic", "Batch status: cancelled")
    assert server.batch["file_counts"]["completed"] == 1
    harness.wait_submit_guard(terminal)
    action(terminal, b"\x1b", "Store details")
    action(terminal, harness.DOWN * 5 + b"\r", "Batch ID")
    terminal.wait("vsfb_synthetic")
    action(terminal, b"\r\r", "cancelled")
    action(terminal, b"\x1b", "Store details")
    action(terminal, harness.DOWN * 2 + b"\r", "Days since last activity")
    submit_editor(terminal, "never", "Store updated.")
    assert server.store_changes["expires_after"] is None
    harness.wait_submit_guard(terminal)
    action(terminal, harness.DOWN * 6 + b"\r", "Confirm store ID")
    submit_editor(terminal, "wrong", "The ID does not match.")
    assert server.count("DELETE", update_path) == 0
    terminal.send(b"\t\t")
    submit_editor(terminal, EXISTING_ID, "Store deleted.")
    assert server.count("POST", update_path) == 4
    assert server.count("DELETE", update_path) == 1
    assert server.count("DELETE", update_path + f"/files/{FILE_ID}") == 1
    assert server.count("DELETE", f"/v1/files/{FILE_ID}") == 0, "attachment removal deleted its source"
    terminal.send(b"q")
    terminal.finish(0)


def scenario(name, terminal, server, source, harness):
    if name.startswith("busy-"):
        harness.wait_for_request(terminal, server.started)
        if name == "busy-ctrl-c":
            terminal.send(b"\x03")
            terminal.finish(130)
        else:
            os.kill(terminal.child.pid, signal.SIGTERM)
            terminal.finish(143)
        assert not server.release.is_set(), "fixture response completed before cancellation"
        return
    terminal.wait("Find store by name or ID")
    if name in ("ctrl-c", "sigterm"):
        if name == "ctrl-c":
            terminal.send(b"\x03")
            terminal.finish(130)
        else:
            os.kill(terminal.child.pid, signal.SIGTERM)
            terminal.finish(143)
        return
    if name == "tiny":
        action(terminal, b"\t\r", "Store name")
        paste(terminal, NAME)
        terminal.send(b"\t")
        mark = len(terminal.raw)
        terminal.resize(30, 8)
        terminal.wait("Resize to 40", after=mark)
        terminal.send(b"\r\r")
        drain(terminal, .25)
        assert server.count("POST", "/v1/vector_stores") == 0, "tiny terminal submitted a hidden action"
        mark = len(terminal.raw)
        terminal.resize(90, 34)
        terminal.wait("Store name", after=mark)
        drain(terminal, .1)
        assert server.count("POST", "/v1/vector_stores") == 0, "resize submitted a hidden action"
        terminal.send(b"\x03")
        terminal.finish(130)
        return
    if name.startswith("management"):
        action(terminal, harness.DOWN * 3 + b"\r", "Search stores by name or ID")
        paste(terminal, "existing")
        action(terminal, b"\r\r", "Find store by name or ID")
    action(terminal, b"\r", "Search your files")
    if name.startswith("search"):
        search_scenario(terminal, server, harness)
        return
    if name.startswith("management"):
        management_scenario(name, terminal, server, harness)
        return
    store_id = EXISTING_ID
    if name == "workflow":
        action(terminal, b"\x1b", "Find store by name or ID")
        action(terminal, harness.DOWN + b"\r", "Store name")
        paste(terminal, NAME)
        action(terminal, b"\r\r\r\r", "Store created.")
        assert server.count("POST", "/v1/vector_stores") == 1, "repeated Enter duplicated store creation"
        harness.wait_submit_guard(terminal)
        store_id = CREATED_ID
        open_upload(terminal)
        action(terminal, b"\t\r", FILENAME)
        # The fixture directory contains only this synthetic file and isolated settings.
        entries = sorted(source.parent.iterdir(), key=lambda path: (not path.is_dir(), path.name))
        index = entries.index(source)
        action(terminal, harness.DOWN * index + b"\r", "Local file path")
        terminal.send(b"\r\r\r")
    else:
        upload_path(terminal, source)
        terminal.wait("HTTP 400")
        if name == "attachment-failure":
            terminal.wait("Attach uploaded file")
            assert server.uploads == 1, "attachment failure duplicated the upload"
            assert server.attachments == 1, "attachment retried without user input"
            assert server.count("GET", f"/v1/vector_stores/{store_id}/files/{FILE_ID}") == 0, \
                "attachment lookup started without manual retry"
        else:
            assert server.uploads == 1, "upload retried without user input"
            assert server.attachments == 0, "failed upload reached attachment"
        harness.wait_submit_guard(terminal)
        terminal.send(b"\r\r\r")
    terminal.wait("File indexed. You can search it now.")
    harness.wait_submit_guard(terminal)
    expected_uploads = 2 if name == "upload-failure" else 1
    expected_attachments = 2 if name == "attachment-failure" else 1
    assert server.uploads == expected_uploads, "unexpected upload count after explicit retry"
    assert server.attachments == expected_attachments, "unexpected attachment count after explicit retry"
    file_reads = [row["response_status"] for row in server.records
                  if row["method"] == "GET" and row["path"] == f"/v1/vector_stores/{store_id}/files/{FILE_ID}"]
    expected_reads = [404, 200] if name == "attachment-failure" else [200]
    assert file_reads == expected_reads, "unexpected remote-state check or indexing sequence"
    if name == "workflow":
        action(terminal, b"\t\t\t\r", "completed")
        terminal.wait(FILENAME)
        action(terminal, b"\x1b", "Search your files")
    paste(terminal, QUERY)
    action(terminal, b"\r", FILENAME)
    terminal.wait(TEXT.decode().strip())
    action(terminal, b"\r", "Relevance score: 0.990")
    terminal.wait(TEXT.decode().strip())
    action(terminal, b"\x1b", "Esc edit query")
    action(terminal, b"\x1b", "Search your files")
    assert server.count("POST", f"/v1/vector_stores/{store_id}/search") == 1, "search was duplicated"
    quit_normally(terminal)


def group_exists(pid):
    try:
        os.killpg(pid, 0)
        return True
    except ProcessLookupError:
        return False


def close_terminal(terminal, destination, result):
    failures = []
    result["exit_status_before_cleanup"] = terminal.child.poll()
    result["forced_harness_cleanup"] = terminal.child.returncode is None
    try:
        terminal.save(destination)
    except Exception as error:
        failures.append(error)
    try:
        terminal.close()
    except Exception as error:
        failures.append(error)
    # An output helper must leave with its owning CLI process.
    pid = terminal.child.pid
    deadline = time.monotonic() + 2
    while group_exists(pid) and time.monotonic() < deadline:
        time.sleep(.03)
    if group_exists(pid):
        result["forced_harness_cleanup"] = True
        try:
            os.killpg(pid, signal.SIGKILL)
        except ProcessLookupError:
            # The process group can exit between the liveness check and the signal.
            pass
        deadline = time.monotonic() + 2
        while group_exists(pid) and time.monotonic() < deadline:
            time.sleep(.03)
        failures.append(AssertionError("owned CLI process group survived command exit"))
    result["process_group_absent"] = not group_exists(pid)
    if failures:
        result["pass"] = False
        raise failures[0]


def run_case(args, harness, name):
    result = {"case": name, "pass": False}
    terminal, server, worker = None, None, None
    original_directory = Path.cwd()
    try:
        with tempfile.TemporaryDirectory(prefix="vector-workspace-") as temporary:
            private = Path(temporary).resolve()
            source = private / FILENAME
            source.write_bytes(TEXT)
            for directory in ("home", "cache", "config", "tmp"):
                (private / directory).mkdir(mode=0o700)
            server = Fixture(name)
            worker = threading.Thread(target=server.serve, name="vector-workspace-fixture")
            worker.start()
            env = {"PATH": "/usr/bin:/bin", "HOME": str(private / "home"),
                   "XDG_CONFIG_HOME": str(private / "config"), "XDG_CACHE_HOME": str(private / "cache"),
                   "TMPDIR": str(private / "tmp"), "LANG": "en_US.UTF-8", "TERM": "xterm-256color",
                   "GOMAXPROCS": "2",
                   "OPENAI_API_KEY": KEY, "OPENAI_BASE_URL": server.endpoint,
                   "OPENAI_PROJECT": "proj_synthetic", "OPENAI_ORG_ID": "org_synthetic",
                   "OPENAI_PICKER_SHELL": "off", "NO_COLOR": "1"}
            if args.theme != "mono":
                env.pop("NO_COLOR")
                env.update({"COLORTERM": "truecolor", "COLORFGBG": "15;0" if args.theme == "dark" else "0;15"})
            commands = ["--project", "proj_synthetic", "--organization", "org_synthetic",
                        "--header", "X-Workspace-Fixture: synthetic", "vector-stores"]
            if name != "workflow":
                commands.append("manage")
            os.chdir(private)
            # Keep the owned PTY outside the child session's controlling terminal.
            # Darwin must retain its slave descriptor for post-exit termios checks.
            width, height = (40, 12) if name == "search-narrow" else (90, 34)
            terminal = terminal_class(harness, args.theme)(str(args.binary), commands, env, width=width, height=height)
            try:
                scenario(name, terminal, server, source, harness)
                assert CANARY not in terminal.text(), "API message escaped into terminal output"
                assert KEY not in terminal.text(), "fake credential escaped into terminal output"
                assert not server.failures, server.failures
                result.update({"pass": True, "exit_status": terminal.child.returncode,
                               "requests": list(server.records), "fixture_failures": list(server.failures)})
            finally:
                close_terminal(terminal, args.output / name, result)
    except Exception as error:
        result["pass"] = False
        result["error"] = type(error).__name__ + ": " + str(error)
    finally:
        os.chdir(original_directory)
        if server is not None:
            server.release.set()
            server.stopped.set()
            if worker is not None and worker.ident is not None:
                worker.join(5)
                result["server_thread_joined"] = not worker.is_alive()
            server.server_close()
            result["requests"] = list(server.records)
            result["fixture_failures"] = list(server.failures)
            if server.failures or worker is not None and worker.is_alive():
                result["pass"] = False
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--source", required=True, help="Full source commit identity")
    parser.add_argument("--source-state", choices=("clean", "dirty"), default="dirty")
    parser.add_argument("--output", type=Path, required=True, help="Fresh evidence directory outside the repository")
    parser.add_argument("--case", choices=(*CASES, "all"), default="all")
    parser.add_argument("--theme", choices=("mono", "dark", "light"), default="mono")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source):
        parser.error("--source requires a full commit identity")
    args.binary = args.binary.resolve(strict=True)
    args.output = args.output.resolve()
    root = Path(__file__).resolve().parents[3]
    if root == args.output or root in args.output.parents:
        parser.error("--output must stay outside the repository")
    args.output.mkdir(parents=True, mode=0o700, exist_ok=False)
    sources = [Path(__file__).resolve(), root / "scripts/image_picker_harness.py",
               root / "internal/app/vector_store_workspace.go", root / "internal/app/command.go",
               *sorted((root / "internal/commands/vectorstores").glob("*.go"))]
    report = {"source_commit": args.source, "source_state": args.source_state, "theme": args.theme,
              "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
              "platform": platform.platform(), "python": platform.python_version(),
              "source_sha256": {str(path.relative_to(root)): hashlib.sha256(path.read_bytes()).hexdigest()
                                for path in sources},
              "evidence_scope": "Owned native PTY, synthetic HTTP, request counts, byte output, and terminal restoration.",
              "limits": "No live API calls, graphical terminal, font acceptance, or emulator viewport assertions.",
              "cases": []}
    harness = load_harness(root)
    # Signals set a flag. The next PTY read unwinds through owned-resource cleanup.
    # A signal during process creation cannot interrupt ownership registration.
    handlers = {number: signal.signal(number, lambda *_: INTERRUPTED.set())
                for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
    try:
        for name in CASES if args.case == "all" else (args.case,):
            if INTERRUPTED.is_set():
                return 130
            result = run_case(args, harness, name)
            report["cases"].append(result)
            (args.output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
            print(json.dumps({"case": name, "pass": result["pass"]}))
            if not result["pass"]:
                return 130 if INTERRUPTED.is_set() else 1
        return 0
    finally:
        for number, handler in handlers.items():
            signal.signal(number, handler)


if __name__ == "__main__":
    raise SystemExit(main())
