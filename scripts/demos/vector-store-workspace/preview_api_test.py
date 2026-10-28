#!/usr/bin/env python3
"""Focused loopback preview contracts. Run directly with Python 3."""

import contextlib
import http.client
import importlib.util
import io
import json
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import threading
import unittest

MODULE_PATH = Path(__file__).with_name("preview_api.py")
SPEC = importlib.util.spec_from_file_location("vector_preview_api", MODULE_PATH)
preview = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(preview)


class PreviewTests(unittest.TestCase):
    def setUp(self):
        self.now = 1792600000.0
        self.server = preview.create_server(lambda: self.now)
        self.thread = threading.Thread(target=self.server.serve_forever, kwargs={"poll_interval": .01})
        self.thread.start()
        self.addCleanup(self.cleanup)
        self.base = "/v1/vector_stores/" + preview.SEED_STORE_ID

    def cleanup(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        self.assertFalse(self.thread.is_alive())

    def request(self, method, path, body=None, headers=None, raw=None):
        connection = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=3)
        fields = {"Authorization": "Bearer " + preview.PREVIEW_KEY}
        if body is not None:
            raw = json.dumps(body).encode()
            fields["Content-Type"] = "application/json"
        fields.update(headers or {})
        try:
            connection.request(method, path, raw, fields)
            response = connection.getresponse()
            data = response.read()
            self.assertEqual(response.getheader("Cache-Control"), "no-store")
            return response.status, json.loads(data)
        finally:
            connection.close()

    def upload(self, text=b"A silver kite has nine green ribbons.\n", name="sample.txt"):
        boundary = "preview-test-boundary"
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="purpose"\r\n\r\nassistants\r\n'
                f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
                'Content-Type: text/plain\r\n\r\n').encode() + text + f"\r\n--{boundary}--\r\n".encode()
        return self.request("POST", "/v1/files", raw=body,
                            headers={"Content-Type": "multipart/form-data; boundary=" + boundary})

    def test_manifest_is_local_and_contains_no_key(self):
        status, manifest = self.request("GET", "/__preview__/manifest")
        self.assertEqual(status, 200)
        self.assertEqual(self.server.server_address[0], "127.0.0.1")
        self.assertEqual(manifest["endpoint"], self.server.endpoint)
        self.assertFalse(manifest["live_api"])
        self.assertFalse(manifest["persistence"])
        self.assertEqual(manifest["search"], "lexical_word_overlap")
        self.assertNotIn(preview.PREVIEW_KEY, json.dumps(manifest))

    def test_seed_search_and_content(self):
        status, result = self.request("POST", self.base + "/search", {"query": "How many ribbons does the copper kite have?"})
        self.assertEqual(status, 200)
        self.assertEqual(result["data"][0]["file_id"], preview.SEED_FILE_ID)
        self.assertEqual(result["data"][0]["content"][0]["text"], preview.SEED_TEXT)
        status, content = self.request("GET", self.base + "/files/" + preview.SEED_FILE_ID + "/content")
        self.assertEqual(status, 200)
        self.assertEqual(content["data"][0]["text"], preview.SEED_TEXT)

    def test_create_update_expiration_metadata(self):
        status, store = self.request("POST", "/v1/vector_stores", {"name": "New preview"})
        self.assertEqual(status, 200)
        path = "/v1/vector_stores/" + store["id"]
        status, updated = self.request("POST", path, {"name": "Renamed", "metadata": {"topic": "demo"},
                                                      "expires_after": {"anchor": "last_active_at", "days": 1}})
        self.assertEqual(status, 200)
        self.assertEqual(updated["name"], "Renamed")
        self.assertEqual(updated["metadata"], {"topic": "demo"})
        self.assertEqual(updated["expires_at"], int(self.now) + 86400)
        status, updated = self.request("POST", path, {"expires_after": None, "metadata": {}})
        self.assertEqual(status, 200)
        self.assertIsNone(updated["expires_at"])
        self.assertEqual(updated["metadata"], {})

    def test_failed_store_update_is_atomic(self):
        status, _ = self.request("POST", self.base, {"name": "Must not persist", "metadata": {"bad": []}})
        self.assertEqual(status, 400)
        _, item = self.request("GET", self.base)
        self.assertEqual(item["name"], "Copper kite demo")

    def test_upload_attach_index_and_search(self):
        status, file = self.upload()
        self.assertEqual(status, 200)
        file_path = self.base + "/files/" + file["id"]
        status, attached = self.request("POST", self.base + "/files", {"file_id": file["id"],
            "attributes": {"revision": 7, "enabled": True}, "chunking_strategy": {
                "type": "static", "static": {"max_chunk_size_tokens": 400, "chunk_overlap_tokens": 100}}})
        self.assertEqual(status, 200)
        self.assertEqual(attached["status"], "in_progress")
        self.assertNotIn("_ready_at", attached)
        self.now += 1
        _, attached = self.request("GET", file_path)
        self.assertEqual(attached["status"], "completed")
        _, store = self.request("GET", self.base)
        self.assertEqual(store["file_counts"]["completed"], 2)
        self.assertEqual(store["usage_bytes"], len(preview.SEED_TEXT.encode()) + file["bytes"])
        status, result = self.request("POST", self.base + "/search", {"query": "silver ribbons", "filters": {
            "type": "and", "filters": [{"type": "eq", "key": "enabled", "value": True},
                                         {"type": "gte", "key": "revision", "value": 7}]}})
        self.assertEqual(status, 200)
        self.assertEqual([row["file_id"] for row in result["data"]], [file["id"]])

    def test_unmatched_search_is_empty(self):
        status, result = self.request("POST", self.base + "/search", {"query": "unrelated zebras"})
        self.assertEqual(status, 200)
        self.assertEqual(result["data"], [])
        self.assertFalse(result["has_more"])

    def test_attachment_removal_preserves_source(self):
        path = self.base + "/files/" + preview.SEED_FILE_ID
        status, changed = self.request("POST", path, {"attributes": {"approved": True, "revision": 8}})
        self.assertEqual(status, 200)
        self.assertEqual(changed["attributes"], {"approved": True, "revision": 8})
        status, result = self.request("DELETE", path)
        self.assertEqual(status, 200)
        self.assertTrue(result["deleted"])
        self.assertEqual(self.request("GET", path)[0], 404)
        self.assertEqual(self.request("GET", "/v1/files/" + preview.SEED_FILE_ID)[0], 200)

    def test_store_removal_preserves_source(self):
        status, result = self.request("DELETE", self.base)
        self.assertEqual(status, 200)
        self.assertTrue(result["deleted"])
        self.assertEqual(self.request("GET", self.base)[0], 404)
        self.assertEqual(self.request("GET", "/v1/files/" + preview.SEED_FILE_ID)[0], 200)

    def test_batch_cancel_preserves_completed_files(self):
        _, first = self.upload(name="first.txt")
        _, second = self.upload(name="second.txt")
        status, batch = self.request("POST", self.base + "/file_batches", {"file_ids": [first["id"], second["id"]]})
        self.assertEqual(status, 200)
        path = self.base + "/file_batches/" + batch["id"]
        self.now += 1.1
        status, result = self.request("POST", path + "/cancel")
        self.assertEqual(status, 200)
        self.assertEqual(result["file_counts"], {"total": 2, "completed": 1, "in_progress": 0, "failed": 0, "cancelled": 1})
        self.now += 10
        _, batch = self.request("GET", path)
        self.assertEqual(batch["status"], "cancelled")
        _, files = self.request("GET", path + "/files")
        self.assertEqual({row["status"] for row in files["data"]}, {"completed", "cancelled"})

    def test_invalid_batch_has_no_partial_attachment(self):
        _, first = self.upload()
        status, _ = self.request("POST", self.base + "/file_batches", {"file_ids": [first["id"], "file_unknown"]})
        self.assertEqual(status, 404)
        self.assertEqual(self.request("GET", self.base + "/files/" + first["id"])[0], 404)

    def test_pagination_and_unknown_cursor(self):
        ids = []
        for name in ("one", "two", "three"):
            _, item = self.request("POST", "/v1/vector_stores", {"name": name})
            ids.append(item["id"])
        _, first = self.request("GET", "/v1/vector_stores?limit=2&order=desc")
        self.assertEqual([row["id"] for row in first["data"]], list(reversed(ids))[:2])
        self.assertTrue(first["has_more"])
        _, second = self.request("GET", "/v1/vector_stores?limit=2&after=" + first["last_id"])
        self.assertEqual([row["id"] for row in second["data"]], [ids[0], preview.SEED_STORE_ID])
        self.assertFalse(second["has_more"])
        self.assertEqual(self.request("GET", "/v1/vector_stores?after=absent")[0], 400)

    def test_isolated_sessions(self):
        with preview.create_server() as other:
            self.request("POST", "/v1/vector_stores", {"name": "Private session mutation"})
            self.assertEqual(len(other.state.stores), 1)

    def test_text_only_upload_and_filename_metadata(self):
        self.assertEqual(self.upload(b"\xffbinary")[0], 400)
        self.assertEqual(self.upload(b"null\x00bytes")[0], 400)
        status, file = self.upload(b"safe text", "../../preview-not-written.txt")
        self.assertEqual(status, 200)
        self.assertEqual(file["filename"], "preview-not-written.txt")

    def test_preview_upload_limits(self):
        status, _ = self.upload(b"x" * (preview.MAX_FILE_BYTES + 1))
        self.assertEqual(status, 413)
        for index in range(7):
            self.assertEqual(self.upload(b"x" * preview.MAX_FILE_BYTES, str(index) + ".txt")[0], 200)
        self.assertEqual(self.upload(b"x" * preview.MAX_FILE_BYTES)[0], 413)

    def test_rejects_foreign_credentials_origins_hosts(self):
        for headers, status in (({"Authorization": "Bearer synthetic-wrong-preview-key"}, 401),
                                ({"Host": "untrusted.invalid"}, 403),
                                ({"Origin": "https://untrusted.invalid"}, 403),
                                ({"Cookie": "synthetic_cookie=value"}, 403)):
            with self.subTest(headers=tuple(headers)):
                self.assertEqual(self.request("GET", "/v1/vector_stores", headers=headers)[0], status)
        self.assertEqual(self.request("POST", "/v1/responses", {"input": "synthetic"})[0], 404)

    def test_malformed_json_does_not_leak_or_mutate(self):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            status, result = self.request("POST", self.base, raw=b'{"name":"SYNTHETIC_PRIVATE_CANARY",invalid}',
                                          headers={"Content-Type": "application/json"})
        self.assertEqual(status, 400)
        self.assertNotIn("SYNTHETIC_PRIVATE_CANARY", json.dumps(result) + output.getvalue() + errors.getvalue())
        self.assertEqual(len(self.server.state.stores), 1)

    def test_nonfinite_json_is_rejected(self):
        status, _ = self.request("POST", self.base + "/search", raw=b'{"query":"kite","max_num_results":NaN}',
                                headers={"Content-Type": "application/json"})
        self.assertEqual(status, 400)

    def test_expired_store_remains_readable_and_deletable(self):
        self.request("POST", self.base, {"expires_after": {"anchor": "last_active_at", "days": 1}})
        self.now += 86401
        _, result = self.request("GET", self.base)
        self.assertEqual(result["status"], "expired")
        _, file = self.upload()
        self.assertEqual(self.request("POST", self.base + "/files", {"file_id": file["id"]})[0], 400)
        self.assertEqual(self.request("DELETE", self.base)[0], 200)


@unittest.skipIf(os.name == "nt", "Inherited parent-pipe executable checks require POSIX.")
class StandaloneTests(unittest.TestCase):
    def test_parent_pipe_eof_and_signal_cleanup(self):
        for stop in ("eof", "signal"):
            with self.subTest(stop=stop):
                read_fd, write_fd = os.pipe()
                # Match supervisors that block shutdown while recording a child.
                previous_mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM, signal.SIGHUP})
                try:
                    process = subprocess.Popen([sys.executable, "-B", str(MODULE_PATH), "--parent-fd", str(read_fd)],
                                               pass_fds=(read_fd,), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                finally:
                    signal.pthread_sigmask(signal.SIG_SETMASK, previous_mask)
                os.close(read_fd)
                try:
                    ready, _, _ = select.select([process.stdout], [], [], 5)
                    self.assertTrue(ready, "Preview did not emit its startup manifest.")
                    manifest = json.loads(process.stdout.readline())
                    self.assertFalse(manifest["live_api"])
                    if stop == "eof":
                        os.close(write_fd)
                        write_fd = None
                    else:
                        process.send_signal(signal.SIGTERM)
                    output, errors = process.communicate(timeout=5)
                    self.assertEqual(process.returncode, 0)
                    self.assertEqual(output, b"")
                    self.assertEqual(errors, b"")
                finally:
                    if write_fd is not None:
                        os.close(write_fd)
                    if process.poll() is None:
                        process.kill()
                        process.communicate(timeout=5)


if __name__ == "__main__":
    unittest.main(verbosity=2)
