#!/usr/bin/env python3
"""In-memory vector-store preview. This module never makes outbound requests.

Preview contract: uploads contain UTF-8 text, at most 1 MiB per file and 8 MiB
per session. Search uses word overlap, not embeddings. Nothing is persisted or
logged. These preview-only limits do not change the CLI or public API contract.
Call create_server(), own its serving thread, then shutdown()/server_close().
The standalone process requires an inherited parent pipe and stops on pipe EOF.
"""

import argparse
import copy
from email import policy
from email.parser import BytesParser
import http.server
import json
import math
import os
import re
import select
import signal
import stat
import threading
import time
from urllib.parse import parse_qs, urlsplit

PREVIEW_KEY = "synthetic-vector-preview-only"
MAX_FILE_BYTES = 1024 * 1024
MAX_SESSION_BYTES = 8 * MAX_FILE_BYTES
MAX_BODY_BYTES = 2 * MAX_FILE_BYTES
SEED_STORE_ID = "vs_preview_copper_kite"
SEED_FILE_ID = "file_preview_copper_kite"
SEED_TEXT = "The copper kite has seven blue ribbons.\n"
STOP_WORDS = frozenset("a an and are does for have how in is many of on the to what with".split())


class PreviewError(Exception):
    def __init__(self, message, status=400):
        super().__init__(message)
        self.status = status


def require(condition, message="The preview could not accept this request.", status=400):
    if not condition:
        raise PreviewError(message, status)


def attributes(value, strings_only=False):
    require(isinstance(value, dict) and len(value) <= 16, "Use an object with at most 16 attributes.")
    for key, item in value.items():
        require(isinstance(key, str) and 0 < len(key) <= 64, "Attribute names must contain 1–64 characters.")
        allowed = isinstance(item, str) or not strings_only and (
            isinstance(item, (bool, int)) or isinstance(item, float) and math.isfinite(item))
        require(allowed, "Use string, number, or boolean attribute values.")
        require(not isinstance(item, str) or len(item) <= 512, "Attribute strings must contain at most 512 characters.")
    return copy.deepcopy(value)


def chunking(value):
    if value is None or value == {"type": "auto"}:
        return {"type": "static", "static": {"max_chunk_size_tokens": 800, "chunk_overlap_tokens": 400}}
    require(isinstance(value, dict) and set(value) == {"type", "static"} and value["type"] == "static",
            "Use auto or static chunking in the preview.")
    settings = value["static"]
    require(isinstance(settings, dict) and set(settings) == {"max_chunk_size_tokens", "chunk_overlap_tokens"})
    size, overlap = settings["max_chunk_size_tokens"], settings["chunk_overlap_tokens"]
    require(type(size) is int and 100 <= size <= 4096 and type(overlap) is int and 0 <= overlap <= size // 2,
            "Use 100–4096 tokens and an overlap no greater than half the chunk size.")
    return copy.deepcopy(value)


def expiration(value):
    if value is None:
        return None
    require(isinstance(value, dict) and set(value) == {"anchor", "days"})
    require(value["anchor"] == "last_active_at" and type(value["days"]) is int and 1 <= value["days"] <= 365,
            "Use last_active_at with 1–365 days in the preview.")
    return copy.deepcopy(value)


def page(items, query):
    try:
        limit = int(query.get("limit", ["20"])[0])
    except ValueError:
        raise PreviewError("Use an integer page limit.") from None
    require(1 <= limit <= 100)
    order = query.get("order", ["desc"])[0]
    require(order in ("asc", "desc"))
    rows = list(items)
    if order == "desc":
        rows.reverse()
    after = query.get("after", [None])[0]
    if after:
        positions = [i for i, row in enumerate(rows) if row["id"] == after]
        require(bool(positions), "The preview page cursor does not exist.")
        rows = rows[positions[0] + 1:]
    selected = rows[:limit]
    return {"object": "list", "data": selected, "has_more": len(rows) > limit,
            "first_id": selected[0]["id"] if selected else None,
            "last_id": selected[-1]["id"] if selected else None}


def matches_filter(attrs, condition):
    if condition is None:
        return True
    require(isinstance(condition, dict))
    operation = condition.get("type")
    if operation in ("and", "or"):
        parts = condition.get("filters")
        require(isinstance(parts, list) and 0 < len(parts) <= 16)
        results = [matches_filter(attrs, part) for part in parts]
        return all(results) if operation == "and" else any(results)
    require(operation in ("eq", "ne", "gt", "gte", "lt", "lte", "in", "nin"),
            "This filter operation is unavailable in the preview.")
    key = condition.get("key")
    require(isinstance(key, str) and "value" in condition)
    if key not in attrs:
        return False
    actual, expected = attrs[key], condition["value"]
    if operation in ("in", "nin"):
        require(isinstance(expected, list))
        return (actual in expected) == (operation == "in")
    if operation == "eq":
        return type(actual) is type(expected) and actual == expected
    if operation == "ne":
        return type(actual) is not type(expected) or actual != expected
    require(type(expected) in (int, float) and math.isfinite(expected))
    if type(actual) not in (int, float):
        return False
    return {"gt": actual > expected, "gte": actual >= expected,
            "lt": actual < expected, "lte": actual <= expected}[operation]


class PreviewState:
    """One server owns all state; a serial request handler avoids mutation races."""

    def __init__(self, clock=time.time):
        self.clock = clock
        self.sequence = 0
        self.stores, self.files, self.texts, self.attachments, self.batches = {}, {}, {}, {}, {}
        self.new_file("copper-kite.txt", SEED_TEXT, SEED_FILE_ID)
        self.new_file("preview-notes.txt", "The preview keeps files in memory until this session ends.\n")
        self.new_store({"name": "Copper kite demo"}, SEED_STORE_ID)
        self.attach(SEED_STORE_ID, {"file_id": SEED_FILE_ID}, delay=0)

    def next_id(self, prefix):
        self.sequence += 1
        return f"{prefix}_preview_{self.sequence:06d}"

    def new_file(self, filename, text, file_id=None):
        size = len(text.encode("utf-8"))
        require(size <= MAX_FILE_BYTES, "Preview files must contain at most 1 MiB of UTF-8 text.", 413)
        require(sum(item["bytes"] for item in self.files.values()) + size <= MAX_SESSION_BYTES,
                "This preview reached its 8 MiB session limit. Start a new preview session.", 413)
        file_id = file_id or self.next_id("file")
        item = {"id": file_id, "object": "file", "created_at": int(self.clock()), "purpose": "assistants",
                "filename": filename, "bytes": size, "status": "processed", "status_details": None, "expires_at": None}
        self.files[file_id], self.texts[file_id] = item, text
        return copy.deepcopy(item)

    def store_changes(self, changes):
        require(set(changes) <= {"name", "metadata", "expires_after"}, "This store option is unavailable in the preview.")
        result = {}
        if "name" in changes:
            require(isinstance(changes["name"], str) and 0 < len(changes["name"]) <= 256,
                    "Store names must contain 1–256 characters.")
            result["name"] = changes["name"]
        if "metadata" in changes:
            result["metadata"] = attributes(changes["metadata"], strings_only=True)
        if "expires_after" in changes:
            result["expires_after"] = expiration(changes["expires_after"])
        return result

    def new_store(self, body, store_id=None):
        changes = self.store_changes(body)
        now = int(self.clock())
        store_id = store_id or self.next_id("vs")
        self.stores[store_id] = {"id": store_id, "object": "vector_store", "name": "Untitled preview store",
                                 "created_at": now, "last_active_at": now, "status": "completed", "metadata": {},
                                 "expires_after": None, "expires_at": None, **changes}
        self.attachments[store_id] = {}
        return self.store(store_id)

    def store(self, store_id):
        require(store_id in self.stores, "This preview store does not exist.", 404)
        self.progress(store_id)
        item = copy.deepcopy(self.stores[store_id])
        attached = list(self.attachments[store_id].values())
        item["file_counts"] = self.counts(attached)
        item["usage_bytes"] = sum(row["usage_bytes"] for row in attached if row["status"] == "completed")
        policy_value = item["expires_after"]
        item["expires_at"] = item["last_active_at"] + policy_value["days"] * 86400 if policy_value else None
        if item["expires_at"] is not None and item["expires_at"] <= self.clock():
            item["status"] = "expired"
        return item

    def progress(self, store_id):
        for row in self.attachments[store_id].values():
            if row["status"] == "in_progress" and row["_ready_at"] <= self.clock():
                row["status"] = "completed"

    @staticmethod
    def counts(items):
        result = dict.fromkeys(("in_progress", "completed", "failed", "cancelled"), 0)
        for row in items:
            result[row["status"]] += 1
        result["total"] = len(items)
        return result

    @staticmethod
    def public(item):
        return copy.deepcopy({key: value for key, value in item.items() if not key.startswith("_")})

    def validate_attach(self, store_id, body):
        require(self.store(store_id)["status"] != "expired", "This preview store has expired.")
        require(set(body) <= {"file_id", "attributes", "chunking_strategy"})
        require(isinstance(body.get("file_id"), str) and body["file_id"] in self.files,
                "This preview source file does not exist.", 404)
        return attributes(body.get("attributes", {})), chunking(body.get("chunking_strategy"))

    def attach(self, store_id, body, delay=.5):
        attrs, strategy = self.validate_attach(store_id, body)
        file_id = body["file_id"]
        require(file_id not in self.attachments[store_id], "This file is already attached to this preview store.", 409)
        row = {"id": file_id, "object": "vector_store.file", "vector_store_id": store_id,
               "created_at": int(self.clock()), "status": "in_progress" if delay else "completed",
               "usage_bytes": self.files[file_id]["bytes"], "last_error": None,
               "attributes": attrs, "chunking_strategy": strategy, "_ready_at": self.clock() + delay}
        self.attachments[store_id][file_id] = row
        self.stores[store_id]["last_active_at"] = int(self.clock())
        return self.public(row)

    def attached(self, store_id, file_id):
        self.store(store_id)
        require(file_id in self.attachments[store_id], "This preview attachment does not exist.", 404)
        return self.attachments[store_id][file_id]

    def batch(self, store_id, batch_id):
        self.store(store_id)
        require((store_id, batch_id) in self.batches, "This preview batch does not exist.", 404)
        item = self.batches[store_id, batch_id]
        rows = [self.attachments[store_id][file_id] for file_id in item["_files"] if file_id in self.attachments[store_id]]
        result = self.public(item)
        result["file_counts"] = self.counts(rows)
        if result["status"] != "cancelled" and not result["file_counts"]["in_progress"]:
            result["status"] = "completed"
        return result

    def create_batch(self, store_id, body):
        require(set(body) <= {"file_ids", "attributes", "chunking_strategy"})
        files = body.get("file_ids")
        require(isinstance(files, list) and 0 < len(files) <= 500 and all(isinstance(x, str) for x in files))
        require(len(files) == len(set(files)), "Select each file only once.")
        options = {key: value for key, value in body.items() if key != "file_ids"}
        for file_id in files:
            self.validate_attach(store_id, {"file_id": file_id, **options})
            require(file_id not in self.attachments[store_id], "A selected file is already attached.", 409)
        batch_id = self.next_id("vsfb")
        for i, file_id in enumerate(files):
            self.attach(store_id, {"file_id": file_id, **options}, delay=1 + i)
        self.batches[store_id, batch_id] = {"id": batch_id, "object": "vector_store.files_batch",
            "vector_store_id": store_id, "created_at": int(self.clock()), "status": "in_progress", "_files": files[:]}
        return self.batch(store_id, batch_id)

    def search(self, store_id, body):
        self.store(store_id)
        require(set(body) <= {"query", "max_num_results", "filters", "ranking_options", "rewrite_query"})
        query = body.get("query")
        require(isinstance(query, str) and bool(query.strip()), "Enter a search query.")
        maximum = body.get("max_num_results", 10)
        require(type(maximum) is int and 1 <= maximum <= 50)
        ranking = body.get("ranking_options", {})
        require(isinstance(ranking, dict) and set(ranking) <= {"ranker", "score_threshold"})
        threshold = ranking.get("score_threshold", 0)
        require(type(threshold) in (int, float) and 0 <= threshold <= 1)
        words = set(re.findall(r"\w+", query.casefold())) - STOP_WORDS
        rows = []
        for file_id, attached in self.attachments[store_id].items():
            if attached["status"] != "completed" or not matches_filter(attached["attributes"], body.get("filters")):
                continue
            text = self.texts[file_id]
            overlap = words & set(re.findall(r"\w+", text.casefold()))
            score = len(overlap) / len(words) if words else 0
            if overlap and score >= threshold:
                rows.append({"file_id": file_id, "filename": self.files[file_id]["filename"], "score": score,
                             "attributes": copy.deepcopy(attached["attributes"]), "content": [{"type": "text", "text": text}]})
        rows.sort(key=lambda row: (-row["score"], row["file_id"]))
        self.stores[store_id]["last_active_at"] = int(self.clock())
        return {"object": "vector_store.search_results.page", "search_query": [query],
                "data": rows[:maximum], "has_more": len(rows) > maximum, "next_page": None}


class PreviewServer(http.server.HTTPServer):
    allow_reuse_address = False

    def __init__(self, clock=time.time):
        self.state = PreviewState(clock)
        super().__init__(("127.0.0.1", 0), PreviewHandler)
        self.timeout = .2

    @property
    def endpoint(self):
        return f"http://127.0.0.1:{self.server_port}/v1"

    def manifest(self):
        return {"kind": "vector-store-preview", "version": 1, "endpoint": self.endpoint,
                "manifest_url": f"http://127.0.0.1:{self.server_port}/__preview__/manifest",
                "live_api": False, "persistence": False, "search": "lexical_word_overlap",
                "max_file_bytes": MAX_FILE_BYTES, "max_session_bytes": MAX_SESSION_BYTES,
                "seed_store_id": SEED_STORE_ID, "seed_file_id": SEED_FILE_ID}

    def handle_error(self, request, client_address):
        # Unexpected exceptions must never print incoming text, filenames, or headers.
        pass


class PreviewHandler(http.server.BaseHTTPRequestHandler):
    server_version = "VectorPreview/1"
    sys_version = ""

    def log_message(self, *_):
        pass

    def setup(self):
        self.request.settimeout(2)
        super().setup()

    def respond(self, status, body):
        payload = json.dumps(body, allow_nan=False).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-Content-Type-Options", "nosniff")
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        self.wfile.write(payload)

    def read_body(self):
        require(not self.headers.get("Transfer-Encoding"), "Preview requests must include a content length.")
        lengths = self.headers.get_all("Content-Length", [])
        require(len(lengths) == 1 and lengths[0].isdigit())
        length = int(lengths[0])
        require(length <= MAX_BODY_BYTES, "This request exceeds the preview body limit.", 413)
        body = self.rfile.read(length)
        require(len(body) == length, "The preview received an incomplete request.")
        return body

    def json_body(self):
        require(self.headers.get_content_type() == "application/json")
        try:
            body = json.loads(self.read_body(), parse_constant=lambda _: (_ for _ in ()).throw(ValueError()))
        except (ValueError, UnicodeError, RecursionError):
            raise PreviewError("Send a JSON object without non-finite numbers.") from None
        require(isinstance(body, dict))
        return body

    def upload(self):
        require(self.headers.get_content_type() == "multipart/form-data", "Use a multipart file upload.")
        content_type = self.headers.get("Content-Type", "")
        try:
            message = BytesParser(policy=policy.default).parsebytes(
                b"Content-Type: " + content_type.encode("ascii") + b"\r\n\r\n" + self.read_body())
        except (ValueError, UnicodeError):
            raise PreviewError("The preview could not parse this upload.") from None
        require(message.is_multipart() and not message.defects)
        parts = {}
        for part in message.iter_parts():
            name = part.get_param("name", header="content-disposition")
            require(name in ("file", "purpose") and name not in parts and not part.is_multipart())
            parts[name] = part
        require(set(parts) == {"file", "purpose"})
        require(parts["purpose"].get_payload(decode=True) == b"assistants", "Use the assistants file purpose in this preview.")
        filename = parts["file"].get_filename()
        require(isinstance(filename, str) and bool(filename), "Choose a file with a filename.")
        # Paths are display metadata only. They are never opened or written by this server.
        filename = re.split(r"[/\\]", filename)[-1]
        raw = parts["file"].get_payload(decode=True)
        require(isinstance(raw, bytes) and len(raw) <= MAX_FILE_BYTES,
                "Preview files must contain at most 1 MiB of UTF-8 text.", 413)
        try:
            text = raw.decode("utf-8")
        except UnicodeError:
            raise PreviewError("Preview uploads must contain UTF-8 text.") from None
        require("\x00" not in text, "Preview uploads must contain UTF-8 text without NUL bytes.")
        return self.server.state.new_file(filename, text)

    def dispatch(self):
        require(self.headers.get_all("Host", []) == [f"127.0.0.1:{self.server.server_port}"],
                "Use the loopback preview endpoint.", 403)
        require(not self.headers.get("Origin") and not self.headers.get("Cookie"),
                "Browser requests are unavailable in this preview.", 403)
        url = urlsplit(self.path)
        require(not url.scheme and not url.netloc and not url.fragment, "Use a relative preview API path.")
        if self.command == "GET" and url.path == "/__preview__/manifest":
            return self.server.manifest()
        require(self.headers.get_all("Authorization", []) in ([], ["Bearer " + PREVIEW_KEY]),
                "This preview accepts only its synthetic key.", 401)
        parts = url.path.strip("/").split("/")
        require(parts[0] == "v1", "This API is unavailable in the local preview.", 404)
        method, state, query = self.command, self.server.state, parse_qs(url.query)
        if parts == ["v1", "files"]:
            if method == "POST":
                return self.upload()
            if method == "GET":
                require(query.get("purpose", ["assistants"]) == ["assistants"])
                return page(copy.deepcopy(list(state.files.values())), query)
        if len(parts) == 3 and parts[:2] == ["v1", "files"] and method == "GET":
            require(parts[2] in state.files, "This preview source file does not exist.", 404)
            return copy.deepcopy(state.files[parts[2]])
        if parts == ["v1", "vector_stores"]:
            if method == "POST":
                return state.new_store(self.json_body())
            if method == "GET":
                return page([state.store(store_id) for store_id in state.stores], query)
        if len(parts) < 3 or parts[:2] != ["v1", "vector_stores"]:
            raise PreviewError("This API is unavailable in the local preview.", 404)
        store_id = parts[2]
        state.store(store_id)
        if len(parts) == 3:
            if method == "GET":
                return state.store(store_id)
            if method == "POST":
                state.stores[store_id].update(state.store_changes(self.json_body()))
                return state.store(store_id)
            if method == "DELETE":
                del state.stores[store_id], state.attachments[store_id]
                for key in list(state.batches):
                    if key[0] == store_id:
                        del state.batches[key]
                return {"id": store_id, "object": "vector_store.deleted", "deleted": True}
        if parts[3:] == ["search"] and method == "POST":
            return state.search(store_id, self.json_body())
        if parts[3:] == ["files"]:
            if method == "POST":
                return state.attach(store_id, self.json_body())
            if method == "GET":
                return page([state.public(row) for row in state.attachments[store_id].values()], query)
        if len(parts) >= 5 and parts[3] == "files":
            file_id = parts[4]
            row = state.attached(store_id, file_id)
            if len(parts) == 6 and parts[5] == "content" and method == "GET":
                require(row["status"] == "completed", "This preview file has not finished indexing.")
                return {"object": "list", "data": [{"type": "text", "text": state.texts[file_id]}], "has_more": False}
            if len(parts) == 5:
                if method == "GET":
                    return state.public(row)
                if method == "POST":
                    body = self.json_body()
                    require(set(body) == {"attributes"})
                    row["attributes"] = attributes(body["attributes"])
                    return state.public(row)
                if method == "DELETE":
                    del state.attachments[store_id][file_id]
                    return {"id": file_id, "object": "vector_store.file.deleted", "deleted": True}
        if parts[3:] == ["file_batches"] and method == "POST":
            return state.create_batch(store_id, self.json_body())
        if len(parts) >= 5 and parts[3] == "file_batches":
            batch_id = parts[4]
            batch = state.batch(store_id, batch_id)
            saved = state.batches[store_id, batch_id]
            if len(parts) == 5 and method == "GET":
                return batch
            if parts[5:] == ["files"] and method == "GET":
                return page([state.public(state.attachments[store_id][file_id]) for file_id in saved["_files"]
                             if file_id in state.attachments[store_id]], query)
            if parts[5:] == ["cancel"] and method == "POST":
                for file_id in saved["_files"]:
                    row = state.attachments[store_id].get(file_id)
                    if row and row["status"] == "in_progress":
                        row["status"] = "cancelled"
                saved["status"] = "cancelled"
                return state.batch(store_id, batch_id)
        raise PreviewError("This API is unavailable in the local preview.", 404)

    def handle_preview(self):
        try:
            result = self.dispatch()
            self.respond(200, result)
        except PreviewError as error:
            self.respond(error.status, {"error": {"message": str(error), "type": "invalid_request_error"}})
        except (BrokenPipeError, ConnectionResetError, TimeoutError):
            pass
        except Exception:
            # No request-derived exception string escapes into diagnostics.
            try:
                self.respond(400, {"error": {"message": "The local preview rejected this request.", "type": "invalid_request_error"}})
            except (BrokenPipeError, ConnectionResetError, TimeoutError):
                pass

    do_GET = do_POST = do_DELETE = handle_preview


def create_server(clock=time.time):
    """Return an unstarted server. The caller owns serving, shutdown, and close."""
    return PreviewServer(clock)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--parent-fd", type=int, required=True, help="Inherited pipe; EOF ends this preview.")
    args = parser.parse_args()
    try:
        parent_mode = os.fstat(args.parent_fd).st_mode
    except OSError:
        parser.error("parent-fd must name an open inherited pipe")
    if not stat.S_ISFIFO(parent_mode):
        parser.error("parent-fd must name an open inherited pipe")
    stopped = threading.Event()
    shutdown_signals = set()
    for name in ("SIGINT", "SIGTERM", "SIGHUP"):
        if hasattr(signal, name):
            shutdown_signal = getattr(signal, name)
            shutdown_signals.add(shutdown_signal)
            signal.signal(shutdown_signal, lambda *_: stopped.set())
    # Supervisors can block signals while recording newly spawned children.
    # The standalone preview owns these handlers and must restore delivery.
    if hasattr(signal, "pthread_sigmask"):
        signal.pthread_sigmask(signal.SIG_UNBLOCK, shutdown_signals)
    with create_server() as server:
        print(json.dumps(server.manifest()), flush=True)
        while not stopped.is_set():
            ready, _, _ = select.select([args.parent_fd, server.socket], [], [], .2)
            if args.parent_fd in ready and not os.read(args.parent_fd, 4096):
                break
            if server.socket in ready:
                server.handle_request()


if __name__ == "__main__":
    main()
