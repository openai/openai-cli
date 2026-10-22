"""Exercise the generated Kitty adapter against real private files and FIFOs."""

import importlib.util
import os
import secrets
import shutil
import stat
import string
import sys
import types
import unittest
import unittest.mock

sys.dont_write_bytecode = True
handler = types.ModuleType("kittens.tui.handler")
handler.result_handler = lambda **options: lambda function: function
sys.modules["kittens.tui.handler"] = handler
spec = importlib.util.spec_from_file_location("adapter", sys.argv.pop(1))
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class AdapterTest(unittest.TestCase):
    def setUp(self):
        session = "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(10))
        self.directory = f"/tmp/openai-click-{os.getuid()}.{session}"
        os.mkdir(self.directory, 0o700)
        self.ready = self.directory + "/ready"
        self.requests = self.directory + "/requests"
        self.epoch = "a" * 32
        self.generation = "b" * 32
        self.uri = f"openai-insert://{session}/{self.generation}/2"
        os.mkfifo(self.requests, 0o600)
        self.reader = os.open(self.requests, os.O_RDONLY | os.O_NONBLOCK)
        self.write_ready(f"{self.epoch} {self.generation} {os.getpid()} 17\n".encode())

    def tearDown(self):
        if self.reader is not None:
            os.close(self.reader)
        shutil.rmtree(self.directory)

    def write_ready(self, data):
        with open(self.ready, "wb") as output:
            output.write(data)
        os.chmod(self.ready, 0o600)

    def request(self, uri=None, window=17):
        return adapter.request_insertion(self.uri if uri is None else uri, window)

    def test_request_contains_only_current_epoch_generation_and_identifier(self):
        self.assertTrue(self.request())
        self.assertEqual(os.read(self.reader, 4096), f"{self.epoch} {self.generation} 2\n".encode())

    def test_wrong_window_and_stale_generation_are_rejected(self):
        self.assertFalse(self.request(window=18))
        self.assertFalse(self.request(uri=self.uri.replace(self.generation, "c" * 32)))
        self.assertEqual(os.read(self.reader, 4096), b"")

    def test_another_kitty_process_with_the_same_window_id_is_rejected(self):
        self.write_ready(f"{self.epoch} {self.generation} {os.getpid() + 1} 17\n".encode())
        self.assertFalse(self.request())
        self.assertEqual(os.read(self.reader, 4096), b"")

    def test_missing_ready_rejects_busy_shell(self):
        os.unlink(self.ready)
        self.assertFalse(self.request())

    def test_malformed_urls_are_rejected(self):
        for uri in [
            self.uri + "?command=anything", self.uri + "#fragment", self.uri + "\n",
            self.uri.replace("/2", "/0"), self.uri.replace("/2", "/129"),
            self.uri.replace("/2", "/02"), self.uri.replace("/2", "/%32"),
            self.uri.replace("openai-insert://", "file://"),
            "openai-insert://../../" + self.generation + "/2",
        ]:
            with self.subTest(uri=uri):
                self.assertFalse(self.request(uri=uri))

    def test_malformed_ready_is_rejected(self):
        for data in [b"", b"x" * 257, b"a b 17\n", f"{self.epoch} {self.generation} 17\n".encode(),
                     f"{self.epoch} {self.generation} {os.getpid()} 17\nextra".encode()]:
            with self.subTest(data=data):
                self.write_ready(data)
                self.assertFalse(self.request())

    def test_permissions_are_required(self):
        for path, mode, original in [(self.directory, 0o755, 0o700), (self.ready, 0o644, 0o600), (self.requests, 0o644, 0o600)]:
            with self.subTest(path=path):
                os.chmod(path, mode)
                self.assertFalse(self.request())
                os.chmod(path, original)

    def test_directory_symlink_is_rejected(self):
        moved = self.directory + ".moved"
        os.rename(self.directory, moved)
        os.symlink(moved, self.directory)
        try:
            self.assertFalse(self.request())
        finally:
            os.unlink(self.directory)
            os.rename(moved, self.directory)

    def test_ready_symlink_hardlink_and_fifo_are_rejected(self):
        moved = self.ready + ".moved"
        os.rename(self.ready, moved)
        os.symlink(moved, self.ready)
        self.assertFalse(self.request())
        os.unlink(self.ready)
        os.link(moved, self.ready)
        self.assertFalse(self.request())
        os.unlink(self.ready)
        os.mkfifo(self.ready, 0o600)
        self.assertFalse(self.request())

    def test_requests_symlink_and_regular_file_are_rejected_without_writes(self):
        os.unlink(self.requests)
        os.symlink(self.ready, self.requests)
        self.assertFalse(self.request())
        os.unlink(self.requests)
        with open(self.requests, "wb") as output:
            output.write(b"preserve")
        os.chmod(self.requests, 0o600)
        self.assertFalse(self.request())
        with open(self.requests, "rb") as source:
            self.assertEqual(source.read(), b"preserve")

    def test_absent_reader_rejects_without_blocking(self):
        os.close(self.reader)
        self.reader = None
        self.assertFalse(self.request())

    def test_full_fifo_rejects_without_blocking(self):
        writer = os.open(self.requests, os.O_WRONLY | os.O_NONBLOCK)
        try:
            for _ in range(1024):
                try:
                    os.write(writer, b"x" * 4096)
                except BlockingIOError:
                    break
            else:
                self.fail("could not fill the bounded FIFO")
            self.assertFalse(self.request())
        finally:
            os.close(writer)

    def test_file_owner_is_checked(self):
        info = types.SimpleNamespace(st_mode=stat.S_IFREG | 0o600, st_uid=os.getuid() + 1)
        self.assertFalse(adapter._private(info, stat.S_ISREG, 0o600, os.getuid()))

    def check_closed_descriptors(self, *, window=17, close_failure=None):
        opened, closed = [], []
        real_open, real_close = os.open, os.close

        def track_open(*args, **kwargs):
            descriptor = real_open(*args, **kwargs)
            opened.append(descriptor)
            return descriptor

        def track_close(descriptor):
            closed.append(descriptor)
            real_close(descriptor)
            if len(closed) == close_failure:
                raise OSError("synthetic close failure")

        try:
            with unittest.mock.patch.object(adapter.os, "open", side_effect=track_open), \
                    unittest.mock.patch.object(adapter.os, "close", side_effect=track_close):
                result = self.request(window=window)
            self.assertEqual(closed, list(reversed(opened)))
            for descriptor in opened:
                with self.assertRaises(OSError):
                    os.fstat(descriptor)
            return result, len(opened)
        finally:
            # A failing implementation still leaves this test's descriptors owned.
            for descriptor in set(opened) - set(closed):
                real_close(descriptor)

    def test_success_and_early_rejection_close_every_opened_descriptor(self):
        self.assertEqual(self.check_closed_descriptors(), (True, 3))
        self.assertEqual(self.check_closed_descriptors(window=18), (False, 2))

    def test_close_failure_does_not_skip_other_descriptors(self):
        for position in range(1, 4):
            with self.subTest(close_position=position):
                self.assertEqual(self.check_closed_descriptors(close_failure=position), (False, 3))


unittest.main()
