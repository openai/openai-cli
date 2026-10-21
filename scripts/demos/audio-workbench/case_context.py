"""Own bounded audio demo resources, terminal state, and capture operations."""

import codecs
from contextlib import contextmanager
import errno
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import select
import signal
import subprocess
import sys
import tempfile
import termios
import threading
import time

NAVIGATION_CASES = ("navigation", "navigation-compact")
FAILURE_CASES = ("failure", "failure-compact")
OBSERVED_CASES = NAVIGATION_CASES + FAILURE_CASES


class HarnessInterrupted(BaseException):
    pass


def group_exists(pgid):
    try:
        os.killpg(pgid, 0)
    except ProcessLookupError:
        return False
    return True


def serve_fixture(server, stopped):
    while not stopped.is_set():
        server.handle_request()


def fixture_class(fixture):
    class OwnedSpeechFixture(fixture.SpeechFixture):
        # Set this before requests. ThreadingMixIn.server_close joins its
        # existing registry even if this flag changes during cleanup.
        block_on_close = False

        def process_request(self, request, client_address):
            handler = threading.Thread(target=self.process_request_thread, args=(request, client_address))
            handler.daemon = self.daemon_threads
            with self.lock:
                self.handler_threads.add(handler)
            try:
                handler.start()
            except BaseException:
                with self.lock:
                    self.handler_threads.discard(handler)
                self.shutdown_request(request)
                raise

    return OwnedSpeechFixture


def terminal_class(picker):
    class Terminal(picker.Terminal):
        def __init__(self, context, *arguments, **options):
            self.context = context
            with self.context.run.protect_ownership():
                super().__init__(*arguments, **options)
                self.harness_closed = False
                self.case_result = self.context.result
                self.cleanup = {"child_pid": self.child.pid, "pgid": self.child.pid, "close_called": False,
                                "forced_harness_cleanup": False, "group_absent_verified": False, "error": None,
                                "case": self.context.result["case"]}
                self.context.terminals.append(self)
                self.context.run.report["owned_terminal_cleanup"].append(self.cleanup)
                self.navigation_observer = None
                self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
                self.decoder_finished = False
                if self.context.result["case"] in OBSERVED_CASES:
                    self.navigation_observer = self.context.run.observer_factory(*self.initial_size)
                    required = ("feed", "viewport_text", "scrollback_lines", "provenance")
                    if not all(callable(getattr(self.navigation_observer, method, None)) for method in required):
                        raise ValueError("Navigation observer adapter has an unsupported interface")
                    self.case_result["navigation_observer"] = self.navigation_observer.provenance()

        def resize(self, width, height, notify=True):
            if getattr(self, "navigation_observer", None) is not None:
                raise ValueError("Navigation observer requires fixed-size sessions; resize was rejected")
            super().resize(width, height, notify=notify)

        def finish(self, expected, timeout=12, expect_picker=True):
            deadline = time.monotonic() + timeout
            previous = getattr(self, "finish_deadline", None)
            self.finish_deadline = deadline
            try:
                while self.child.poll() is None:
                    self.read(.05)
                # An exited child can leave more than three queued PTY chunks.
                # Drain until quiet before reusing every shared exit assertion.
                while True:
                    before = len(self.raw)
                    self.read(.03)
                    if len(self.raw) == before:
                        break
                super().finish(expected, timeout=max(0, deadline-time.monotonic()), expect_picker=expect_picker)
            finally:
                self.finish_deadline = previous

        def close(self):
            if self.harness_closed:
                return
            with self.context.run.protect_ownership():
                self.cleanup["close_called"] = True
                self.cleanup["product_exit_status_before_cleanup"] = self.child.poll()
                self.cleanup["forced_harness_cleanup"] = self.child.returncode is None
                failure = None
                try:
                    super().close()
                except Exception as error:
                    failure = error
                finally:
                    self.harness_closed = True
                try:
                    pgid = self.cleanup["pgid"]
                    assert pgid > 0 and pgid != os.getpgrp(), "Refusing cleanup outside the owned CLI group"
                    if group_exists(pgid):
                        self.cleanup["forced_harness_cleanup"] = True
                        try:
                            os.killpg(pgid, signal.SIGKILL)
                        except ProcessLookupError:
                            # The process group exited between the existence check and killpg.
                            pass
                    self.child.wait(timeout=1)
                    deadline = time.monotonic() + 2
                    while group_exists(pgid) and time.monotonic() < deadline:
                        time.sleep(.03)
                    self.cleanup["group_absent_verified"] = not group_exists(pgid)
                    assert self.cleanup["group_absent_verified"], "Owned CLI process group survived harness cleanup"
                except Exception as error:
                    failure = error
                if self.cleanup["forced_harness_cleanup"] or failure is not None:
                    self.case_result["pass"] = False
                if failure is not None:
                    self.cleanup["error"] = type(failure).__name__ + ": " + str(failure)
                    raise failure

        def finish_decode(self):
            if not self.decoder_finished:
                tail = self.decoder.decode(b"", final=True)
                if tail:
                    self.events.append([time.monotonic()-self.started, "o", tail])
                self.decoder_finished = True

        def read(self, duration=.05):
            deadline = getattr(self, "finish_deadline", None)
            if deadline is not None:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise AssertionError("Terminal output did not finish before the exit deadline")
                duration = min(duration, remaining)
            if not select.select([self.master], [], [], duration)[0]:
                return
            try:
                fresh = os.read(self.master, 65536)
            except OSError as error:
                if error.errno == errno.EIO:
                    self.finish_decode()
                    return
                raise
            if not fresh:
                self.finish_decode()
                return
            self.raw.extend(fresh)
            decoded = self.decoder.decode(fresh)
            if decoded:
                self.events.append([time.monotonic()-self.started, "o", decoded])
            if self.navigation_observer is not None:
                self.navigation_observer.feed(fresh)
            pending = getattr(self, "queries", b"") + fresh
            pattern = rb"\x1b\[(?:6n|0?c|\?7\$p)|\x1b\]11;\?(?:\x07|\x1b\\)"
            while match := re.search(pattern, pending):
                query = match.group()
                if query.startswith(b"\x1b]11"):
                    rgb = b"ffff/ffff/ffff" if self.context.args.theme == "light" else b"1818/1818/1818"
                    reply = b"\x1b]11;rgb:" + rgb + b"\x1b\\"
                elif query == b"\x1b[6n":
                    reply = b"\x1b[1;1R"
                elif query == b"\x1b[?7$p":
                    reply = b"\x1b[?7;1$y"
                else:
                    reply = b"\x1b[?1;2c"
                self.send(reply)
                pending = pending[match.end():]
            self.queries = pending[-128:]
            if self.context.args.emit:
                sys.stdout.buffer.write(fresh)
                sys.stdout.buffer.flush()

        def save(self, path):
            path.with_suffix(".raw").write_bytes(self.raw)
            header = {"version": 2, "width": self.initial_size[0], "height": self.initial_size[1],
                      "title": "Audio workbench | synthetic loopback API", "env": {"TERM": "xterm-256color"}}
            path.with_suffix(".cast").write_text("\n".join(json.dumps(event) for event in [header, *self.events]) + "\n")
            if self.navigation_observer is not None:
                self.case_result["navigation_observer"] = self.navigation_observer.provenance()

    return Terminal


class Harness:
    """Own signal handling and final cleanup across independently registered cases."""

    def __init__(self, args, picker, fixture, observer_factory=None):
        self.args, self.picker, self.fixture = args, picker, fixture
        self.observer_factory = observer_factory
        self.terminal_class = terminal_class(picker)
        self.fixture_class = fixture_class(fixture)
        self.cases = []
        self.interruption = {"signal": None, "protected": 0, "finalizing": False}
        self.report = {"source": args.source, "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
                       "platform": platform.system() + " " + platform.machine(), "theme": args.theme,
                       "scope": "PTY terminal replay; synthetic audio; fake player; no audible playback claim",
                       "cases": [], "owned_terminal_cleanup": [],
                       "all_owned_terminal_groups_absent": False, "interruption_signal": None}

    @property
    def terminals(self):
        return [terminal for case in self.cases for terminal in case.terminals]

    def write_report(self):
        (self.args.output / "results.json").write_text(json.dumps(self.report, indent=2, ensure_ascii=False) + "\n")

    def interrupt_harness(self, number, _frame):
        if self.interruption["signal"] is None:
            self.interruption["signal"] = number
            self.report["interruption_signal"] = number
        if not self.interruption["protected"] and not self.interruption["finalizing"]:
            raise HarnessInterrupted()

    @contextmanager
    def protect_ownership(self):
        # Keep ownership registration atomic with respect to Python signal handlers.
        # Child processes retain the caller's original signal mask.
        self.interruption["protected"] += 1
        try:
            yield
        finally:
            self.interruption["protected"] -= 1
        if self.interruption["signal"] is not None and not self.interruption["protected"] and not self.interruption["finalizing"]:
            raise HarnessInterrupted()

    @contextmanager
    def lifecycle(self):
        previous = {number: signal.getsignal(number) for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
        try:
            for number in previous:
                signal.signal(number, self.interrupt_harness)
            yield
        except HarnessInterrupted:
            if self.report["cases"]:
                self.report["cases"][-1]["pass"] = False
        finally:
            self.interruption["finalizing"] = True
            failures = []
            for terminal in self.terminals:
                try:
                    terminal.close()
                except Exception as error:
                    failures.append(type(error).__name__)
                if terminal.cleanup["forced_harness_cleanup"] or terminal.cleanup["error"] is not None:
                    terminal.case_result["pass"] = False
            for context in self.cases:
                state = context.server_state
                if state is None:
                    continue
                try:
                    context.close_fixture()
                    if not state["closed"]:
                        raise AssertionError("Fixture cleanup did not complete")
                except Exception as error:
                    state["result"]["pass"] = False
                    failures.append(type(error).__name__)
            self.report["all_owned_terminal_groups_absent"] = all(
                entry["group_absent_verified"] for entry in self.report["owned_terminal_cleanup"])
            self.report["interruption_signal"] = self.interruption["signal"]
            if self.interruption["signal"] is not None and self.report["cases"]:
                self.report["cases"][-1]["pass"] = False
            try:
                (self.args.output / "results.json").write_text(json.dumps(self.report, indent=2, ensure_ascii=False) + "\n")
            finally:
                for number, handler in previous.items():
                    signal.signal(number, handler)
            if failures or not self.report["all_owned_terminal_groups_absent"]:
                raise AssertionError("Owned harness cleanup failed: " + ", ".join(failures))
            if self.interruption["signal"] is not None:
                raise SystemExit(128 + self.interruption["signal"])
            if any(entry["forced_harness_cleanup"] for entry in self.report["owned_terminal_cleanup"]):
                raise AssertionError("Harness cleanup forced a CLI process group to stop; product cancellation did not pass")


class CaseContext:
    """Own one scenario's result, private fixture, terminals, and capture state."""

    def __init__(self, run, name):
        self.run, self.name = run, name
        self.args, self.picker, self.fixture = run.args, run.picker, run.fixture
        self.result = {"case": name, "states": [], "pass": False}
        self.terminals = []
        self.server_state = None
        self.terminal = None
        self.navigation = name in NAVIGATION_CASES
        self.failure = name in FAILURE_CASES
        self.busy = name in ("busy-ctrl-c", "busy-sigterm")
        run.cases.append(self)
        run.report["cases"].append(self.result)

    def new_terminal(self, *arguments, **options):
        return self.run.terminal_class(self, *arguments, **options)

    def setup(self):
        programs = self.private / "bin"
        programs.mkdir(mode=0o700)
        playback = self.private / "playback.json"
        playback_clock = self.private / "playback-clock.json"
        # The fake player receives actual generated bytes. It emits no sound.
        # Disable bytecode writes before Python startup can change the cache.
        helper = ("#!" + sys.executable + " -B\nimport hashlib,json,os,sys,time\n"
                  "assert 'OPENAI_API_KEY' not in os.environ and 'FFREPORT' not in os.environ\n"
                  "data=sys.stdin.buffer.read()\n"
                  "stats='-stats' in sys.argv\n"
                  "with open(" + repr(str(playback)) + ", 'a') as out:\n"
                  " out.write(json.dumps({'pid':os.getpid(),'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest(),'stats':stats})+'\\n')\n"
                  "if not stats:\n"
                  " time.sleep(2)\n"
                  " sys.exit(0)\n"
                  "def report(clock):\n"
                  " sys.stderr.write(f'{clock:.3f} M-A: 0.000 fd=0 aq=0KB vq=0KB sq=0B\\r')\n"
                  " sys.stderr.flush()\n"
                  "report(0)\n"
                  "last=0\n"
                  "deadline=time.monotonic()+20\n"
                  "while time.monotonic()<deadline:\n"
                  " try:\n"
                  "  with open(" + repr(str(playback_clock)) + ") as source:\n"
                  "   value=json.load(source)\n"
                  " except (FileNotFoundError,json.JSONDecodeError):\n"
                  "  value={}\n"
                  " if value.get('pid')==os.getpid() and last<value.get('clock',0)<=2:\n"
                  "  last=value['clock']\n"
                  "  report(last)\n"
                  " time.sleep(.01)\n"
                  "sys.exit(4)\n")
        (programs / "ffplay").write_text(helper)
        (programs / "ffplay").chmod(0o700)
        busy = self.name in ("busy-ctrl-c", "busy-sigterm")
        failure = self.name in FAILURE_CASES
        stopped = threading.Event()
        with self.run.protect_ownership():
            server = self.run.fixture_class(partial=busy, auth_failure=failure)
            self.server_state = {"server": server, "worker": None, "stopped": stopped,
                                 "result": self.result, "closed": False}
            server.timeout = .1
            worker = threading.Thread(target=serve_fixture, args=(server, stopped))
            self.server_state["worker"] = worker
            worker.start()
        env = {"PATH": str(programs) + ":/usr/bin:/bin", "HOME": str(self.private),
               "XDG_CACHE_HOME": str(self.private / "cache"), "XDG_CONFIG_HOME": str(self.private / "config"),
               "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "GOMAXPROCS": "2",
               "OPENAI_PICKER_SHELL": "off", "OPENAI_API_KEY": "synthetic-demo-key",
               "OPENAI_BASE_URL": server.endpoint, "FORCE_COLOR": "1",
               "COLORTERM": "truecolor", "COLORFGBG": "0;15" if self.args.theme == "light" else "15;0"}
        if self.args.theme == "no-color":
            env.update(NO_COLOR="1", FORCE_COLOR="0")
        navigation = self.name in ("navigation", "navigation-compact")
        if navigation:
            env.update(NO_COLOR="1", FORCE_COLOR="0")
            self.result["effective_theme"] = "no-color"

        self.programs, self.playback, self.server, self.env = programs, playback, server, env
        self.playback_clock = playback_clock

    def public(self, arguments):
        command = subprocess.run([str(self.args.binary), *arguments], env=self.env, stdin=subprocess.DEVNULL,
                                 capture_output=True, timeout=12)
        assert command.returncode == 0 and command.stderr == b"", "Local audio command failed"
        return command.stdout

    def start_form(self):
        if self.navigation:
            self.seeded_entry_list = [self.seed_history(self.private, self.env)]
        width, height = (40, 12) if self.name in ("compact", "navigation-compact", "failure-compact") else (90, 34)
        if self.name == "wide":
            width = 186
        self.terminal = self.new_terminal(str(self.args.binary), ["audio", "speak"], self.env, width=width, height=height)
        self.result["initial_size"] = [width, height]
        self.wait(self.terminal, "Text to speech")

    def finish_form(self):
        terminal, result, server, case = self.terminal, self.result, self.server, self.name
        navigation, failure, busy = self.navigation, self.failure, self.busy
        terminal.save(self.args.output / case)
        final = self.screen(terminal)
        (self.args.output / (case + "-final.txt")).write_text(final)
        if navigation or failure:
            observer = terminal.navigation_observer
            retained = observer.scrollback_lines()
            (self.args.output / (case + "-scrollback.json")).write_text(json.dumps(retained, ensure_ascii=False, indent=2) + "\n")
            prefix = "navigation" if navigation else "failure"
            result[prefix + "_observer"] = observer.provenance()
            result[prefix + "_viewport_clear"] = not final.strip()
            result[prefix + "_scrollback_clear"] = all(not line.strip() for lines in retained.values() for line in lines)
            result[prefix + "_scrollback_rows"] = {name: len(lines) for name, lines in retained.items()}
            assert not any(event[1] == "r" for event in terminal.events), "Fixed-size observed capture unexpectedly resized"
            assert result[prefix + "_observer"]["bytes_observed"] == len(terminal.raw), "Observer missed captured bytes"
        result["final_form_rows_cleared"] = "Text to speech" not in final
        if case != "stopped-reader":
            assert result["final_form_rows_cleared"], "Exited form left active rows in the final screen"
        if navigation or failure:
            assert result[prefix + "_viewport_clear"], "Observed case left content in its final viewport"
            assert result[prefix + "_scrollback_clear"], "Observed case left owned content in terminal scrollback"
        assert not server.failures, "Fixture rejected a request"
        if case not in ("workflow", "compact", *FAILURE_CASES) and not busy:
            assert not server.records, "Cancellation generated speech"
        result.update(exit_status=terminal.child.returncode, requests=server.records, pass_=True)
        result["pass"] = result.pop("pass_")

    def execute(self, scenario):
        with tempfile.TemporaryDirectory(prefix="audio-workbench-demo-") as temporary:
            self.private = Path(temporary)
            try:
                self.setup()
                if self.name not in ("startup", "print-command"):
                    self.start_form()
                scenario(self)
                if self.terminal is not None:
                    self.finish_form()
            except BaseException:
                self.result["pass"] = False
                raise
            finally:
                try:
                    self.close()
                finally:
                    self.run.write_report()

    def close_fixture(self):
        state = self.server_state
        if state is None:
            return
        if state.get("cleanup_attempted", False):
            return
        with self.run.protect_ownership():
            state["cleanup_attempted"] = True
            server, worker, case_result = state["server"], state["worker"], state["result"]
            deadline = time.monotonic() + 5
            state["stopped"].set()
            server.partial_release.set()
            if worker is not None and worker.ident is not None:
                worker.join(max(0, deadline-time.monotonic()))
            # The request loop has a finite timeout. No shutdown worker or
            # BaseServer.shutdown wait can outlive this cleanup deadline.
            server.server_close()
            with server.lock:
                handlers = list(server.handler_threads)
            for handler in handlers:
                handler.join(max(0, deadline-time.monotonic()))
            cleanup = {"serve_thread_joined": worker is None or not worker.is_alive(), "handler_threads_observed": len(handlers),
                       "handler_threads_joined": all(not handler.is_alive() for handler in handlers)}
            case_result["server_cleanup"] = cleanup
            state["closed"] = cleanup["serve_thread_joined"] and cleanup["handler_threads_joined"]
            if not state["closed"]:
                case_result["pass"] = False
                raise AssertionError("Fixture threads did not stop within the cleanup deadline")

    def close(self):
        failures = []
        try:
            if self.terminal is not None:
                if self.terminal.child.poll() is not None:
                    self.result["exit_status"] = self.terminal.child.returncode
                self.terminal.save(self.args.output / (self.name + "-latest"))
        except Exception as error:
            failures.append(error)
        finally:
            with self.run.protect_ownership():
                # Registration precedes observer setup and return to the caller.
                for terminal in self.terminals:
                    try:
                        terminal.close()
                    except Exception as error:
                        failures.append(error)
                try:
                    self.close_fixture()
                except Exception as error:
                    failures.append(error)
        if failures:
            self.result["pass"] = False
            raise failures[0]

    def screen(self, terminal):
        terminal.save(self.args.output / "current-screen")
        if terminal.navigation_observer is not None:
            return terminal.navigation_observer.viewport_text()
        converted = subprocess.run([self.args.asciinema, "convert", "--overwrite", "-f", "txt",
                                    str(self.args.output / "current-screen.cast"), "-"],
                                   capture_output=True, text=True, check=True, timeout=5,
                                   env={"PATH": "/usr/bin:/bin", "HOME": str(self.args.output),
                                        "ASCIINEMA_STATE_HOME": str(self.args.output / "asciinema-state"),
                                        "ASCIINEMA_CONFIG_HOME": str(self.args.output / "asciinema-config")})
        return converted.stdout

    def drain(self, terminal, duration=.15):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            terminal.read(min(.03, max(0, deadline-time.monotonic())))

    def wait(self, terminal, marker, absent=(), timeout=12):
        deadline = time.monotonic() + timeout
        markers = (marker,) if isinstance(marker, str) else marker
        while True:
            self.drain(terminal, .06)
            visible = self.screen(terminal)
            if any(value in visible for value in markers) and not any(value in visible for value in absent):
                return visible
            if terminal.child.poll() is not None or time.monotonic() > deadline:
                raise AssertionError("Audio form did not reach state " + repr(marker) + ": " + visible[-1800:])

    def mark(self, terminal, state, result):
        self.drain(terminal, .9)  # Also respects the form's documented repeat-submit guard.
        terminal.save(self.args.output / state)
        (self.args.output / (state + ".txt")).write_text(self.screen(terminal))
        result["states"].append({"name": state, "seconds": round(time.monotonic()-terminal.started, 4),
                                 "recorded_at": time.time()})

    def paste(self, terminal, text):
        terminal.send(b"\x1b[200~" + text.encode() + b"\x1b[201~")

    def check_native_modes(self, terminal):
        restored = termios.tcgetattr(terminal.slave)
        initial = list(terminal.initial)
        if sys.platform == "darwin":
            restored[3] &= ~termios.PENDIN
            initial[3] &= ~termios.PENDIN
        assert restored == initial, "CLI did not restore native terminal modes"

    def check_frame_boundaries(self, terminal):
        assert not re.search(rb"\x1b\[\?(?:47|1047|1049)[hl]", terminal.raw), "CLI used an alternate screen"
        assert not re.search(rb"\x1b\[[23]J", terminal.raw), "CLI cleared the screen or scrollback"

    def check_live_parent_terminal(self, terminal, form):
        self.check_native_modes(terminal)
        self.check_frame_boundaries(terminal)
        if form:
            assert terminal.raw.rfind(b"\x1b[?2004l") > terminal.raw.rfind(b"\x1b[?2004h") >= 0, "CLI did not restore bracketed paste"
            assert terminal.raw.rfind(b"\x1b[?25h") > terminal.raw.rfind(b"\x1b[?25l") >= 0, "CLI did not restore cursor visibility"
        else:
            assert b"\x1b[?2004h" not in terminal.raw, "Local command entered an interactive form"

    def process_snapshot(self):
        completed = subprocess.run(["/bin/ps", "-axo", "pid=,ppid=,comm="], capture_output=True,
                                   text=True, check=True, timeout=2,
                                   env={"PATH": "/usr/bin:/bin", "LC_ALL": "C"})
        processes = []
        for line in completed.stdout.splitlines():
            fields = line.split(None, 2)
            assert len(fields) >= 2, "Cannot parse process identity snapshot"
            processes.append({"pid": int(fields[0]), "ppid": int(fields[1]),
                              "comm": fields[2] if len(fields) == 3 else ""})
        return processes

    def check_children_exited(self, children, result):
        started = time.monotonic()
        pids = {child["pid"] for child in children}
        while True:
            remaining = [process for process in self.process_snapshot() if process["pid"] in pids]
            result["owned_child_cleanup"] = {
                "observed": children, "remaining": remaining, "checked_without_signals": True,
                "seconds": round(time.monotonic()-started, 4), "pass": bool(children) and not remaining,
            }
            if not remaining or time.monotonic()-started >= 2:
                break
            time.sleep(.03)
        assert children, "No owned helper was observed; cleanup coverage is missing"
        assert not remaining, "An observed CLI helper survived cancellation"

    def drain_after_exit(self, terminal, result):
        started = last_output = time.monotonic()
        initial_bytes = len(terminal.raw)
        reason = "deadline"
        while time.monotonic()-started < 2:
            before = len(terminal.raw)
            terminal.read(.02)
            if len(terminal.raw) != before:
                last_output = time.monotonic()
            elif terminal.decoder_finished:
                reason = "EOF"
                break
            elif time.monotonic()-last_output >= .2:
                reason = "quiet-200ms"
                break
        result["post_exit_drain"] = {"bytes": len(terminal.raw)-initial_bytes, "reason": reason,
                                     "seconds": round(time.monotonic()-started, 4)}

    def seed_history(self, private, env):
        cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
        history = cache / "openai/audio"
        identity = "0123456789abcdef0123456789abcdef"
        artifact = history / identity
        artifact.mkdir(mode=0o700, parents=True)
        history.chmod(0o700)
        settings = {"input": self.fixture.TEXT, "instructions": self.fixture.INSTRUCTIONS,
                    "model": "gpt-4o-mini-tts", "voice": "coral", "format": "wav", "speed": 1}
        entry = {"id": identity, "created_at": "2026-10-09T00:00:00Z", "settings": settings,
                 "bytes": len(self.fixture.WAV), "sha256": hashlib.sha256(self.fixture.WAV).hexdigest()}
        (artifact / "audio").write_bytes(self.fixture.WAV)
        (artifact / "manifest.json").write_text(json.dumps({"version": 1, "entry": entry}) + "\n")
        for path in artifact.iterdir():
            path.chmod(0o600)
        return entry
