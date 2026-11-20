#!/usr/bin/env python3
"""Exercise the shipping vector store CLI inside a private Windows ConPTY."""

import argparse
from contextlib import contextmanager
import ctypes as c
from ctypes import wintypes as w
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import tempfile
import threading
import time
from types import SimpleNamespace
from urllib.parse import urlsplit

def load_fixture():
    source = Path(__file__).with_name("check.py")
    spec = importlib.util.spec_from_file_location("shipping_vector_fixture", source)
    fixture = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(fixture)
    return fixture


check = load_fixture()


ANSI = re.compile(r'\x1b\][^\x07]*?(?:\x07|\x1b\\)|\x1b\[[0-?]*[ -/]*[@-~]')
QUERY = "How many blue ribbons does the synthetic copper kite have? Include only details from the uploaded source and preserve the full question after resizing."
CASES = ("toggle-resize", "upload-failure", "attachment-failure", "ctrl-c", "busy-ctrl-c")
AFTER = "F15_NATIVE_MODES_RESTORED"
PING = "f15-host-read"
PONG = "F15_NATIVE_INPUT_RESTORED"
PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE = 0x00020016
EXTENDED_STARTUPINFO_PRESENT = 0x00080000
WAIT_TIMEOUT = 258
GENERIC_READ_WRITE = 0xC0000000
FILE_SHARE_READ_WRITE = 0x00000003
OPEN_EXISTING = 3
INVALID_HANDLE_VALUE = c.c_void_p(-1).value


class Coord(c.Structure):
    _fields_ = [("X", c.c_short), ("Y", c.c_short)]


class Startup(c.Structure):
    _fields_ = (
        [(name, kind) for name, kind in (
            ("cb", w.DWORD), ("reserved", w.LPWSTR), ("desktop", w.LPWSTR),
            ("title", w.LPWSTR), ("x", w.DWORD), ("y", w.DWORD),
            ("width", w.DWORD), ("height", w.DWORD), ("chars_x", w.DWORD),
            ("chars_y", w.DWORD), ("fill", w.DWORD), ("flags", w.DWORD),
            ("show", w.WORD), ("reserved_size", w.WORD), ("reserved_bytes", c.c_void_p),
            ("stdin", w.HANDLE), ("stdout", w.HANDLE), ("stderr", w.HANDLE),
        )] + [("attributes", c.c_void_p)]
    )


class Process(c.Structure):
    _fields_ = [("handle", w.HANDLE), ("thread", w.HANDLE),
                ("pid", w.DWORD), ("thread_id", w.DWORD)]


def kernel():
    api = c.WinDLL("kernel32", use_last_error=True)
    size_p = c.POINTER(c.c_size_t)
    void = c.c_void_p
    handle_p = c.POINTER(w.HANDLE)
    definitions = {
        "CreatePipe": (w.BOOL, handle_p, handle_p, void, w.DWORD),
        "CreatePseudoConsole": (c.c_long, Coord, w.HANDLE, w.HANDLE, w.DWORD, handle_p),
        "ResizePseudoConsole": (c.c_long, w.HANDLE, Coord),
        "ClosePseudoConsole": (None, w.HANDLE),
        "InitializeProcThreadAttributeList": (w.BOOL, void, w.DWORD, w.DWORD, size_p),
        "UpdateProcThreadAttribute": (w.BOOL, void, w.DWORD, c.c_size_t, void, c.c_size_t, void, void),
        "DeleteProcThreadAttributeList": (None, void),
        "CreateProcessW": (w.BOOL, w.LPCWSTR, w.LPWSTR, void, void, w.BOOL,
                           w.DWORD, void, w.LPCWSTR, c.POINTER(Startup), c.POINTER(Process)),
        "CreateFileW": (w.HANDLE, w.LPCWSTR, w.DWORD, w.DWORD, void, w.DWORD, w.DWORD, w.HANDLE),
        "GetConsoleMode": (w.BOOL, w.HANDLE, c.POINTER(w.DWORD)),
        "GetConsoleProcessList": (w.DWORD, c.POINTER(w.DWORD), w.DWORD),
        "ReadConsoleW": (w.BOOL, w.HANDLE, void, w.DWORD, c.POINTER(w.DWORD), void),
        "WriteConsoleW": (w.BOOL, w.HANDLE, w.LPCWSTR, w.DWORD, c.POINTER(w.DWORD), void),
        "WaitForSingleObject": (w.DWORD, w.HANDLE, w.DWORD),
        "GetExitCodeProcess": (w.BOOL, w.HANDLE, c.POINTER(w.DWORD)),
        "TerminateProcess": (w.BOOL, w.HANDLE, w.UINT),
        "CloseHandle": (w.BOOL, w.HANDLE),
    }
    for name, (restype, *argtypes) in definitions.items():
        function = getattr(api, name)
        function.restype = restype
        function.argtypes = argtypes
    return api


def require(answer):
    if not answer:
        raise c.WinError(c.get_last_error())


def receipt_path(directory):
    return directory / "console.json"


@contextmanager
def console_stream(api, device, mode):
    # Runner standard handles can refer to redirected pipes even under ConPTY.
    # These devices refer to the witness's attached console.
    import msvcrt

    handle = api.CreateFileW(device, GENERIC_READ_WRITE, FILE_SHARE_READ_WRITE,
                             None, OPEN_EXISTING, 0, None)
    if not handle or handle == INVALID_HANDLE_VALUE:
        raise c.WinError(c.get_last_error(), f"opening attached {device}")
    try:
        access = os.O_RDONLY if mode == "rb" else os.O_WRONLY
        descriptor = msvcrt.open_osfhandle(handle, os.O_BINARY | access)
    except OSError:
        require(api.CloseHandle(handle))
        raise
    try:
        stream = os.fdopen(descriptor, mode, buffering=0)
    except OSError:
        os.close(descriptor)
        raise
    with stream:
        yield stream, handle


def modes(api, input_handle, output_handle):
    result = []
    for handle in (input_handle, output_handle):
        flags = w.DWORD()
        require(api.GetConsoleMode(handle, c.byref(flags)))
        result.append(flags.value)
    return result


def require_shared_console(api, child):
    deadline = time.monotonic() + 3
    processes = (w.DWORD * 16)()
    while True:
        count = api.GetConsoleProcessList(processes, len(processes))
        require(count)
        assert count <= len(processes), "private console process count exceeds the bound"
        pids = processes[:count]
        assert os.getpid() in pids, "witness is missing from the attached console"
        if child.pid in pids:
            return
        assert child.poll() is None, "CLI exited before joining the witness console"
        assert time.monotonic() < deadline, "CLI did not join the witness console"
        time.sleep(.025)


def write_console_line(api, handle, line):
    count = w.DWORD()
    line += "\r\n"
    require(api.WriteConsoleW(handle, line, len(line), c.byref(count), None))
    assert count.value == len(line), "console status line was truncated"


def read_console_line(api, handle):
    buffer = c.create_unicode_buffer(128)
    count = w.DWORD()
    require(api.ReadConsoleW(handle, buffer, len(buffer), c.byref(count), None))
    return buffer[:count.value].rstrip("\r\n")


def witness(binary, endpoint, directory):
    """Run inside ConPTY and observe the console before and after the CLI."""
    api = kernel()
    inherited = ("SystemRoot", "WINDIR", "PATH", "COMSPEC", "PATHEXT")
    env = {name: os.environ[name] for name in inherited if name in os.environ}
    env.update({
        "HOME": str(directory / "home"), "USERPROFILE": str(directory / "home"),
        "APPDATA": str(directory / "config"), "LOCALAPPDATA": str(directory / "cache"),
        "XDG_CONFIG_HOME": str(directory / "config"), "XDG_CACHE_HOME": str(directory / "cache"),
        "TEMP": str(directory / "tmp"), "TMP": str(directory / "tmp"),
        "TERM": "xterm-256color", "NO_COLOR": "1", "OPENAI_PICKER_SHELL": "off",
        "OPENAI_API_KEY": check.KEY, "OPENAI_BASE_URL": endpoint,
    })
    command = [str(binary), "--project", "proj_synthetic", "--organization", "org_synthetic",
               "--header", "X-Workspace-Fixture: synthetic", "vector-stores", "manage"]
    with console_stream(api, "CONIN$", "rb") as (console_input, input_handle):
        with console_stream(api, "CONOUT$", "wb") as (console_output, output_handle):
            before = modes(api, input_handle, output_handle)
            child = subprocess.Popen(command, cwd=directory, env=env, stdin=console_input,
                                     stdout=console_output, stderr=console_output)
            try:
                require_shared_console(api, child)
                exit_code = child.wait(timeout=90)
            finally:
                if child.poll() is None:
                    child.kill()
                    child.wait(timeout=5)
            after = modes(api, input_handle, output_handle)
            receipt = {"exit": exit_code, "before": before, "after": after,
                       "shared_console": True, "input": False}
            receipt_path(directory).write_text(json.dumps(receipt), encoding="utf-8")
            assert after == before, "console input/output modes changed"
            write_console_line(api, output_handle, AFTER)
            assert read_console_line(api, input_handle) == PING, "the same console failed to read a new line"
            receipt["input"] = True
            receipt_path(directory).write_text(json.dumps(receipt), encoding="utf-8")
            write_console_line(api, output_handle, PONG)


class Terminal:
    def __init__(self):
        self.api = kernel()
        self.console = w.HANDLE()
        self.process = Process()
        self.unowned = []
        self.writer = self.reader = self.thread = None
        self.reader_error = None
        self.raw = bytearray()
        self.responses_sent = 0
        self.receipt = None

    def pipe(self):
        handles = w.HANDLE(), w.HANDLE()
        require(self.api.CreatePipe(*(c.byref(handle) for handle in handles), None, 0))
        self.unowned.extend(handles)
        return handles

    def close_handle(self, handle):
        require(self.api.CloseHandle(handle))
        self.unowned.remove(handle)

    def file(self, handle, mode):
        import msvcrt

        access = os.O_RDONLY if mode == "rb" else os.O_WRONLY
        descriptor = msvcrt.open_osfhandle(handle.value, os.O_BINARY | access)
        self.unowned.remove(handle)
        return os.fdopen(descriptor, mode, buffering=0)

    def open(self, binary, server, directory):
        receive, write = self.pipe()
        read, transmit = self.pipe()
        result = self.api.CreatePseudoConsole(Coord(94, 32), receive, transmit, 0, c.byref(self.console))
        assert result == 0, f"CreatePseudoConsole HRESULT {result:#x}"
        self.writer = self.file(write, "wb")
        self.reader = self.file(read, "rb")
        self.thread = threading.Thread(target=self.drain, daemon=True)
        self.thread.start()

        size = c.c_size_t()
        self.api.InitializeProcThreadAttributeList(None, 1, 0, c.byref(size))
        assert size.value > 0, "Windows returned no startup attribute capacity"
        attributes = c.create_string_buffer(size.value)
        require(self.api.InitializeProcThreadAttributeList(attributes, 1, 0, c.byref(size)))
        try:
            # The pseudoconsole startup attribute accepts the HPCON value directly.
            require(self.api.UpdateProcThreadAttribute(
                attributes, 0, PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, self.console, c.sizeof(self.console), None, None))
            startup = Startup()
            startup.cb = c.sizeof(startup)
            startup.attributes = c.cast(attributes, c.c_void_p)
            self.directory = directory
            arguments = [sys.executable, "-I", "-B", str(Path(__file__).resolve()), "--witness",
                         str(binary), server.endpoint, str(directory)]
            command = c.create_unicode_buffer(subprocess.list2cmdline(arguments))
            require(self.api.CreateProcessW(sys.executable, command, None, None, False,
                                            EXTENDED_STARTUPINFO_PRESENT, None, str(directory),
                                            c.byref(startup), c.byref(self.process)))
        finally:
            self.api.DeleteProcThreadAttributeList(attributes)
        require(self.api.CloseHandle(self.process.thread))
        self.process.thread = None
        self.close_handle(receive)
        self.close_handle(transmit)

    def __enter__(self):
        return self

    def __exit__(self, _error_type, error, _traceback):
        try:
            self.close()
        except Exception as cleanup_error:
            if error is None:
                raise
            error.add_note(f"Console cleanup also failed: {cleanup_error}")

    def close(self):
        if self.process.handle:
            try:
                if self.api.WaitForSingleObject(self.process.handle, 0) == WAIT_TIMEOUT:
                    require(self.api.TerminateProcess(self.process.handle, 255))
                    assert self.api.WaitForSingleObject(self.process.handle, 3000) == 0
            finally:
                require(self.api.CloseHandle(self.process.handle))
        if self.writer:
            self.writer.close()
        if self.console:
            self.api.ClosePseudoConsole(self.console)
        for handle in self.unowned[:]:
            self.close_handle(handle)
        if self.thread:
            self.thread.join(4)
            assert not self.thread.is_alive(), "console reader did not stop"
        if self.reader:
            self.reader.close()

    def drain(self):
        try:
            while chunk := self.reader.read(65536):
                if len(self.raw) + len(chunk) > 4 * 1024 * 1024:
                    # Keep consuming until close so the pseudoconsole can drain.
                    self.reader_error = "terminal evidence exceeded its bound"
                    continue
                self.raw.extend(chunk)
        except OSError as error:
            if getattr(error, "winerror", None) not in (109, 232):
                self.reader_error = f"reader failed: {error}"

    def send(self, value):
        assert self.writer.write(value) == len(value), "Windows short write"

    def read(self, duration=.05):
        time.sleep(duration)
        assert self.reader_error is None, self.reader_error
        error_path = self.directory / "console-error.txt"
        if error_path.is_file():
            raise RuntimeError(error_path.read_text(encoding="utf-8"))
        queries = rb"\x1b\[(?:6n|0?c|\?(?:7|1049)\$p)"
        replies = {b"\x1b[6n": b"\x1b[1;1R", b"\x1b[c": b"\x1b[?1;2c",
                   b"\x1b[0c": b"\x1b[?1;2c", b"\x1b[?7$p": b"\x1b[?7;1$y",
                   b"\x1b[?1049$p": b"\x1b[?1049;2$y"}
        observed = re.findall(queries, bytes(self.raw))
        for query in observed[self.responses_sent:]:
            self.send(replies[query])
        self.responses_sent = len(observed)

    def text(self, after=0):
        return ANSI.sub("", bytes(self.raw[after:]).decode("utf-8", "replace"))

    def wait(self, marker, timeout=15, after=0):
        expires = time.monotonic() + timeout
        while marker not in self.text(after):
            assert time.monotonic() < expires, f"missing {marker!r}; terminal tail={self.text()[-1400:]}"
            self.read()

    def resize(self, width, height):
        result = self.api.ResizePseudoConsole(self.console, Coord(width, height))
        assert result == 0, f"ResizePseudoConsole HRESULT {result:#x}"

    def finish(self, expected):
        self.wait(AFTER)
        self.send(PING.encode("ascii") + b"\r")
        self.wait(PONG)
        assert self.api.WaitForSingleObject(self.process.handle, 5000) == 0, "console observer did not exit"
        code = w.DWORD()
        require(self.api.GetExitCodeProcess(self.process.handle, c.byref(code)))
        assert code.value == 0, f"console observer exited {code.value}"
        self.receipt = json.loads(receipt_path(self.directory).read_text(encoding="utf-8"))
        assert self.receipt["exit"] == expected, self.receipt
        assert self.receipt["before"] == self.receipt["after"], self.receipt
        assert self.receipt["shared_console"] and self.receipt["input"], self.receipt


def assert_screen_transition(terminal, mark, *, expanded):
    direction = b"h" if expanded else b"l"
    pattern = rb"\x1b\[\?(?:1049|1047|47)([hl])"
    transitions = re.findall(pattern, terminal.raw[mark:])
    assert transitions and transitions[-1] == direction, (
        "fresh terminal output did not end in the requested screen mode", transitions, direction
    )


def toggle_screen(terminal, *, expanded):
    mark = len(terminal.raw)
    hint = "Ctrl+G compact" if expanded else "Ctrl+G expand"
    check.action(terminal, b"\x07", hint)
    assert_screen_transition(terminal, mark, expanded=expanded)


def zoom_and_resize(terminal, server):
    terminal.wait("Enter open store")
    toggle_screen(terminal, expanded=True)
    toggle_screen(terminal, expanded=False)
    check.action(terminal, b"\r", "Search your files")
    toggle_screen(terminal, expanded=True)
    check.paste(terminal, QUERY)
    mark = len(terminal.raw)
    terminal.resize(39, 11)
    terminal.wait("Resize to 40", after=mark)
    terminal.send(b"\r")
    check.drain(terminal)
    path = f"/v1/vector_stores/{check.EXISTING_ID}/search"
    assert server.count("POST", path) == 0, "tiny terminal submitted a hidden search"
    mark = len(terminal.raw)
    terminal.resize(52, 20)
    terminal.wait("Ctrl+G compact", after=mark)
    mark = len(terminal.raw)
    terminal.resize(94, 32)
    terminal.wait("Ctrl+G compact", after=mark)
    check.action(terminal, b"\r", check.FILENAME)
    searches = [row["body"]["query"] for row in server.records if row["path"] == path]
    assert searches == [QUERY], "resize changed or duplicated the query"
    check.action(terminal, b"\x1b", "Enter search")
    check.action(terminal, b"\x1b", "Enter open store")
    mark = len(terminal.raw)
    terminal.send(b"q")
    terminal.finish(0)
    assert_screen_transition(terminal, mark, expanded=False)


class BusyCancellationHandler(check.Handler):
    def respond(self, status, body):
        server = self.server
        if (server.cancel_verified.is_set() and server.release.is_set()
                and self.command == "GET" and urlsplit(self.path).path == "/v1/vector_stores"
                and status == 200):
            # The CLI has already exited while this response was gated.
            server.withheld_responses += 1
            self.close_connection = True
            return
        super().respond(status, body)


class BusyCancellationFixture(check.Fixture):
    def __init__(self):
        super().__init__("busy-ctrl-c")
        self.cancel_verified = threading.Event()
        self.withheld_responses = 0
        self.RequestHandlerClass = BusyCancellationHandler


def stop_fixture(server, fixture_thread):
    server.stopped.set()
    server.release.set()
    fixture_thread.join(4)
    server.server_close()
    assert not fixture_thread.is_alive(), "fixture thread survived"
    assert not server.failures, server.failures


def run_case(binary, name):
    check.QUERY = QUERY if name == "toggle-resize" else ORIGINAL_QUERY
    if name == "busy-ctrl-c":
        server = BusyCancellationFixture()
    else:
        server = check.Fixture("resize" if name == "toggle-resize" else name)
    if name == "toggle-resize":
        server.attached = True
        server.attached_store = check.EXISTING_ID
    fixture_thread = threading.Thread(target=server.serve, daemon=True)
    fixture_thread.start()
    failure = None
    try:
        with tempfile.TemporaryDirectory(prefix="f15-windows-") as workspace:
            directory = Path(workspace)
            for name_part in ("home", "cache", "config", "tmp"):
                (directory / name_part).mkdir()
            source = directory / check.FILENAME
            source.write_bytes(check.TEXT)
            with Terminal() as terminal:
                terminal.open(binary, server, directory)
                if name == "toggle-resize":
                    zoom_and_resize(terminal, server)
                elif name in ("upload-failure", "attachment-failure"):
                    # Reuse the shipping workflow's loopback assertions and keystrokes.
                    harness = SimpleNamespace(DOWN=b"\x1b[B", wait_submit_guard=lambda term: check.drain(term, 1))
                    check.scenario(name, terminal, server, source, harness)
                else:
                    if name == "busy-ctrl-c":
                        deadline = time.monotonic() + 12
                        while not server.started.is_set():
                            assert time.monotonic() < deadline, "fixture received no busy request"
                            terminal.read()
                    else:
                        terminal.wait("Enter open store")
                        terminal.wait("Ctrl+G expand")
                    assert server.count("GET", "/v1/vector_stores") == 1
                    terminal.send(b"\x03")
                    terminal.finish(130)
                    assert not server.release.is_set(), "fixture gate released before CLI exit"
                assert check.CANARY not in terminal.text() and check.KEY not in terminal.text()
                assert not server.failures, server.failures
        if name == "busy-ctrl-c":
            server.cancel_verified.set()
    except BaseException as error:
        failure = error
        raise
    finally:
        try:
            stop_fixture(server, fixture_thread)
        except Exception as cleanup_error:
            if failure is None:
                raise
            failure.add_note(f"Fixture cleanup also failed: {cleanup_error}")
    result = {"case": name, "pass": True, "console": terminal.receipt,
              "requests": server.records.copy()}
    if name == "busy-ctrl-c":
        assert server.records == [{"method": "GET", "path": "/v1/vector_stores",
                                   "body": None, "response_status": 200}], server.records
        assert server.withheld_responses == 1, "fixture did not withhold the canceled response"
        result["withheld_responses"] = server.withheld_responses
    return result


ORIGINAL_QUERY = check.QUERY


def main():
    if sys.argv[1:2] == ["--witness"]:
        binary, endpoint, directory = sys.argv[2:]
        directory = Path(directory)
        try:
            witness(Path(binary), endpoint, directory)
        except (AssertionError, OSError, subprocess.TimeoutExpired) as error:
            pending = directory / "console-error.pending"
            pending.write_text(f"{type(error).__name__}: {error}", encoding="utf-8")
            pending.replace(directory / "console-error.txt")
            raise
        return
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--shipping", required=True)
    parser.add_argument("--report", required=True, type=Path)
    args = parser.parse_args()
    assert os.name == "nt" and __debug__, "Windows and enabled assertions are required"
    binary = args.binary.resolve(strict=True)
    report = {
        "shipping": args.shipping, "binary_sha256": hashlib.sha256(binary.read_bytes()).hexdigest(),
        "platform": platform.platform(), "architecture": platform.machine(), "cases": [],
    }
    for name in CASES:
        try:
            result = run_case(binary, name)
        except Exception as error:
            details = "\n".join([str(error), *getattr(error, "__notes__", [])])
            result = {"case": name, "pass": False, "error": details}
        report["cases"].append(result)
        args.report.write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
        print(json.dumps(result), flush=True)
    raise SystemExit(0 if all(result["pass"] for result in report["cases"]) else 1)


if __name__ == "__main__":
    main()
