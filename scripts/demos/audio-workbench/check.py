#!/usr/bin/env python3
"""Drive real audio commands in owned PTYs. Requires a prior PTY reservation."""

import argparse
import codecs
from contextlib import contextmanager
import errno
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import select
import shutil
import signal
import subprocess
import sys
import tempfile
import termios
import threading
import time


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", type=Path, required=True)
    parser.add_argument("--source", required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--repo-root", type=Path, default=os.environ.get("DEMO_REPO_ROOT"))
    parser.add_argument("--case", choices=("workflow", "compact", "navigation", "navigation-compact", "failure", "failure-compact",
                                          "ctrl-c", "sigterm", "busy-ctrl-c", "busy-sigterm",
                                          "stopped-reader", "startup", "all"), default="all")
    parser.add_argument("--theme", choices=("dark", "light", "no-color"), default="dark")
    parser.add_argument("--asciinema", default=shutil.which("asciinema"))
    parser.add_argument("--navigation-observer", type=Path,
                        help="External reviewed observer adapter for fixed-size navigation or failure checks")
    parser.add_argument("--emit", action="store_true", help="Forward actual PTY bytes for the shared recorder")
    parser.add_argument("--no-resize", action="store_true", help="Keep the emitted comparison workflow at 90x34")
    args = parser.parse_args()
    root = args.repo_root or Path(__file__).resolve().parents[3]
    picker = load("audio_terminal", root / "scripts/image_picker_harness.py")
    fixture = load("audio_fixture", Path(__file__).with_name("fixture.py"))
    navigation_cases = ("navigation", "navigation-compact")
    failure_cases = ("failure", "failure-compact")
    observed_cases = (*navigation_cases, *failure_cases)
    needs_navigation_observer = args.case in (*observed_cases, "all")
    if not args.asciinema and args.case not in observed_cases:
        raise SystemExit("asciinema is required for real terminal-screen reconstruction")
    if not re.fullmatch(r"[0-9a-f]{40}", args.source):
        raise SystemExit("source must be a complete commit identity")
    if args.emit and args.case != "workflow":
        raise SystemExit("--emit records only the workflow case")
    if args.no_resize and not (args.emit and args.case == "workflow"):
        raise SystemExit("--no-resize applies only to the emitted comparison workflow")
    navigation_adapter = None
    if needs_navigation_observer:
        if args.navigation_observer is None or not args.navigation_observer.is_file():
            raise SystemExit("Navigation and failure cases require --navigation-observer pointing to the reviewed adapter")
        adapter_sha256 = hashlib.sha256(args.navigation_observer.read_bytes()).hexdigest()
        if adapter_sha256 != "6b97dfa956642d7fe3c26bde091b9e932775e72c20973b128923389de4dae79e":
            raise SystemExit("Navigation observer adapter does not match the reviewed source")
        navigation_adapter = load("audio_navigation_observer", args.navigation_observer)
        if not callable(getattr(navigation_adapter, "create_observer", None)):
            raise SystemExit("Navigation observer adapter has no supported factory")
    elif args.navigation_observer is not None:
        raise SystemExit("--navigation-observer applies only to navigation/failure cases or --case all")
    args.output.mkdir(parents=True, exist_ok=False)
    report = {"source": args.source, "binary_sha256": hashlib.sha256(args.binary.read_bytes()).hexdigest(),
              "platform": platform.system() + " " + platform.machine(), "theme": args.theme,
              "scope": "PTY terminal replay; synthetic audio; fake player; no audible playback claim", "cases": [],
              "owned_terminal_cleanup": [], "all_owned_terminal_groups_absent": False, "interruption_signal": None}
    owned_terminals, owned_servers = [], []
    interruption = {"signal": None, "protected": 0, "finalizing": False}

    class HarnessInterrupted(BaseException):
        pass

    def interrupt_harness(number, _frame):
        if interruption["signal"] is None:
            interruption["signal"] = number
            report["interruption_signal"] = number
        if not interruption["protected"] and not interruption["finalizing"]:
            raise HarnessInterrupted()

    @contextmanager
    def protect_ownership():
        # Defer Python interruption during launch and cleanup. Do not change
        # the signal mask inherited by the CLI or its helpers.
        interruption["protected"] += 1
        try:
            yield
        finally:
            interruption["protected"] -= 1
        if interruption["signal"] is not None and not interruption["protected"] and not interruption["finalizing"]:
            raise HarnessInterrupted()

    def group_exists(pgid):
        try:
            os.killpg(pgid, 0)
        except ProcessLookupError:
            return False
        return True

    def serve_fixture(server, stopped):
        while not stopped.is_set():
            server.handle_request()

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

    def close_fixture(state):
        if state.get("cleanup_attempted", False):
            return
        with protect_ownership():
            state["cleanup_attempted"] = True
            server, worker, case_result = state["server"], state["worker"], state["result"]
            deadline = time.monotonic() + 5
            state["stopped"].set()
            server.partial_release.set()
            if worker.ident is not None:
                worker.join(max(0, deadline-time.monotonic()))
            # The request loop has a finite timeout. No shutdown worker or
            # BaseServer.shutdown wait can outlive this cleanup deadline.
            server.server_close()
            with server.lock:
                handlers = list(server.handler_threads)
            for handler in handlers:
                handler.join(max(0, deadline-time.monotonic()))
            cleanup = {"serve_thread_joined": not worker.is_alive(), "handler_threads_observed": len(handlers),
                       "handler_threads_joined": all(not handler.is_alive() for handler in handlers)}
            case_result["server_cleanup"] = cleanup
            state["closed"] = cleanup["serve_thread_joined"] and cleanup["handler_threads_joined"]
            if not state["closed"]:
                case_result["pass"] = False
                raise AssertionError("Fixture threads did not stop within the cleanup deadline")

    @contextmanager
    def harness_lifecycle():
        previous = {number: signal.getsignal(number) for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP)}
        try:
            for number in previous:
                signal.signal(number, interrupt_harness)
            yield
        except HarnessInterrupted:
            if report["cases"]:
                report["cases"][-1]["pass"] = False
        finally:
            interruption["finalizing"] = True
            failures = []
            for terminal in owned_terminals:
                try:
                    terminal.close()
                except Exception as error:
                    failures.append(type(error).__name__)
                if terminal.cleanup["forced_harness_cleanup"] or terminal.cleanup["error"] is not None:
                    terminal.case_result["pass"] = False
            for state in owned_servers:
                try:
                    close_fixture(state)
                    if not state["closed"]:
                        raise AssertionError("Fixture cleanup did not complete")
                except Exception as error:
                    state["result"]["pass"] = False
                    failures.append(type(error).__name__)
            report["all_owned_terminal_groups_absent"] = all(
                entry["group_absent_verified"] for entry in report["owned_terminal_cleanup"])
            report["interruption_signal"] = interruption["signal"]
            if interruption["signal"] is not None and report["cases"]:
                report["cases"][-1]["pass"] = False
            try:
                (args.output / "results.json").write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")
            finally:
                for number, handler in previous.items():
                    signal.signal(number, handler)
            if failures or not report["all_owned_terminal_groups_absent"]:
                raise AssertionError("Owned harness cleanup failed: " + ", ".join(failures))
            if interruption["signal"] is not None:
                raise SystemExit(128 + interruption["signal"])
            if any(entry["forced_harness_cleanup"] for entry in report["owned_terminal_cleanup"]):
                raise AssertionError("Harness cleanup forced a CLI process group to stop; product cancellation did not pass")

    class Terminal(picker.Terminal):
        def __init__(self, *arguments, **options):
            with protect_ownership():
                super().__init__(*arguments, **options)
                self.decoder = codecs.getincrementaldecoder("utf-8")("replace")
                self.decoder_finished = False
                self.harness_closed = False
                self.case_result = result
                self.cleanup = {"child_pid": self.child.pid, "pgid": self.child.pid, "close_called": False,
                                "forced_harness_cleanup": False, "group_absent_verified": False, "error": None,
                                "case": result["case"]}
                owned_terminals.append(self)
                report["owned_terminal_cleanup"].append(self.cleanup)
                self.navigation_observer = None
                if result["case"] in observed_cases:
                    self.navigation_observer = navigation_adapter.create_observer(*self.initial_size)
                    required = ("feed", "viewport_text", "scrollback_lines", "provenance")
                    if not all(callable(getattr(self.navigation_observer, method, None)) for method in required):
                        raise ValueError("Navigation observer adapter has an unsupported interface")
                    self.case_result["navigation_observer"] = self.navigation_observer.provenance()

        def resize(self, width, height, notify=True):
            if getattr(self, "navigation_observer", None) is not None:
                raise ValueError("Navigation observer requires fixed-size sessions; resize was rejected")
            super().resize(width, height, notify=notify)

        def close(self):
            if self.harness_closed:
                return
            with protect_ownership():
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
                    rgb = b"ffff/ffff/ffff" if args.theme == "light" else b"1818/1818/1818"
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
            if args.emit:
                sys.stdout.buffer.write(fresh)
                sys.stdout.buffer.flush()

        def save(self, path):
            path.with_suffix(".raw").write_bytes(self.raw)
            header = {"version": 2, "width": self.initial_size[0], "height": self.initial_size[1],
                      "title": "Audio workbench | synthetic loopback API", "env": {"TERM": "xterm-256color"}}
            path.with_suffix(".cast").write_text("\n".join(json.dumps(event) for event in [header, *self.events]) + "\n")
            if self.navigation_observer is not None:
                self.case_result["navigation_observer"] = self.navigation_observer.provenance()

    def screen(terminal):
        terminal.save(args.output / "current-screen")
        if terminal.navigation_observer is not None:
            return terminal.navigation_observer.viewport_text()
        converted = subprocess.run([args.asciinema, "convert", "--overwrite", "-f", "txt",
                                    str(args.output / "current-screen.cast"), "-"],
                                   capture_output=True, text=True, check=True, timeout=5,
                                   env={"PATH": "/usr/bin:/bin", "HOME": str(args.output),
                                        "ASCIINEMA_STATE_HOME": str(args.output / "asciinema-state"),
                                        "ASCIINEMA_CONFIG_HOME": str(args.output / "asciinema-config")})
        return converted.stdout

    def drain(terminal, duration=.15):
        deadline = time.monotonic() + duration
        while time.monotonic() < deadline:
            terminal.read(min(.03, max(0, deadline-time.monotonic())))

    def wait(terminal, marker, absent=(), timeout=12):
        deadline = time.monotonic() + timeout
        markers = (marker,) if isinstance(marker, str) else marker
        while True:
            drain(terminal, .06)
            visible = screen(terminal)
            if any(value in visible for value in markers) and not any(value in visible for value in absent):
                return visible
            if terminal.child.poll() is not None or time.monotonic() > deadline:
                raise AssertionError("Audio form did not reach state " + repr(marker) + ": " + visible[-1800:])

    def mark(terminal, state, result):
        drain(terminal, .9)  # Also respects the form's documented repeat-submit guard.
        terminal.save(args.output / state)
        (args.output / (state + ".txt")).write_text(screen(terminal))
        result["states"].append({"name": state, "seconds": round(time.monotonic()-terminal.started, 4),
                                 "recorded_at": time.time()})

    def paste(terminal, text):
        terminal.send(b"\x1b[200~" + text.encode() + b"\x1b[201~")

    def failure_case(terminal, server, result, public, env):
        summary = "The API did not accept this key."
        protocol_summary = "HTTP 401: Unauthorized."

        def safe_screen(target=terminal):
            visible = screen(target)
            for canary in fixture.FAILURE_CANARIES:
                assert canary.encode() not in target.raw, "Untrusted API message reached terminal output"
            assert b"synthetic-demo-key" not in target.raw, "Terminal disclosed the fake credential value"
            return " ".join(visible.split())

        def visible_failure(required, absent=(), timeout=12, target=terminal):
            deadline = time.monotonic() + timeout
            while True:
                drain(target, .06)
                visible = safe_screen(target)
                if all(text in visible for text in required) and all(text not in visible for text in absent):
                    return visible
                if target.child.poll() is not None or time.monotonic() > deadline:
                    raise AssertionError("Audio recovery did not expose the required safe state")

        def main_form(target=terminal):
            return visible_failure(("Text to speech", "More actions…"),
                                   absent=(summary, protocol_summary, "HTTP status:", "Request ID:",
                                           "Setup: openai", "Command preview", "F1 details"), target=target)

        def home_snapshot():
            home = Path(env["HOME"])
            snapshot = {}
            for path in home.rglob("*"):
                relative = str(path.relative_to(home))
                if path.is_file():
                    snapshot[relative] = hashlib.sha256(path.read_bytes()).hexdigest()
                elif path.is_dir():
                    snapshot[relative] = "directory"
                else:
                    raise AssertionError("Unexpected synthetic HOME entry during recovery help check")
            return snapshot

        terminal.send(b"\t")
        wait(terminal, ("╭ Instructions ", "› Instructions"))
        visible_failure((fixture.DEFAULT_INSTRUCTIONS,))
        terminal.send(b"\x1b[Z")
        main_form()
        mark(terminal, "failure-initial-form", result)
        paste(terminal, fixture.TEXT)
        mark(terminal, "failure-ready", result)
        assert not server.records, "Editing submitted speech before explicit generation"
        terminal.send(b"\x07")
        visible_failure((summary, "› API setup", "Error details", "Back to form"),
                        absent=(protocol_summary, "HTTP status:", fixture.FAILURE_REQUEST_ID))
        mark(terminal, "failure-recovery", result)
        assert len(server.records) == 1 and server.records[0].get("response_status") == 401
        failed_request = server.records[0]["body"]
        assert failed_request["input"] == fixture.TEXT
        assert failed_request["instructions"] == fixture.DEFAULT_INSTRUCTIONS, "New draft omitted its actual Instructions example"
        assert json.loads(public(["--format", "json", "audio", "history"])) == [], "Failed speech entered History"

        # Inspect the actual in-form canonical guide. This action must neither
        # run setup steps nor mutate HOME, credentials, or request state.
        before_help = home_snapshot()
        terminal.send(b"\r")
        visible_failure(("API setup", "Close this form before following these steps.", "Then reopen audio speak in that shell."))
        mark(terminal, "failure-api-setup", result)
        guide_frames = [safe_screen()]
        for _ in range(32):
            terminal.send(b"\x1b[6~")
            drain(terminal, .08)
            current = safe_screen()
            guide_frames.append(current)
            if current == guide_frames[-2]:
                break
        else:
            raise AssertionError("API setup guide did not reach a bounded scroll endpoint")
        guide = " ".join(guide_frames)
        help_output = public(["help", "setup"])
        help_text = picker.ANSI.sub("", help_output.decode("utf-8", errors="strict"))
        for phrase in ("Set up your API key", "OPENAI_API_KEY", "This is a guide only."):
            assert phrase in guide and phrase in help_text, "API setup panel omitted canonical setup guidance"
        assert b"synthetic-demo-key" not in help_output
        assert all(canary.encode() not in help_output for canary in fixture.FAILURE_CANARIES)
        assert home_snapshot() == before_help, "Reading API setup changed synthetic HOME"
        assert len(server.records) == 1, "Reading API setup reached the speech API"
        (args.output / "failure-setup-help.txt").write_bytes(help_output)
        (args.output / "failure-setup-panel.json").write_text(json.dumps(guide_frames, ensure_ascii=False, indent=2) + "\n")
        result["setup_recovery"] = {"panel_title": "API setup", "canonical_panel_visible": True,
                                    "close_and_reopen_guidance_visible": True,
                                    "verification_arguments": [str(args.binary), "help", "setup"],
                                    "exit_status": 0, "requests_added": 0, "synthetic_home_unchanged": True}
        terminal.send(b"\x1b")
        visible_failure((summary, "› API setup"))
        terminal.send(picker.DOWN + b"\r")
        visible_failure(("Error details",))
        terminal.send(picker.END)
        visible_failure(("Error details", "HTTP status: 401", "Request ID: " + fixture.FAILURE_REQUEST_ID, "Esc back"))
        mark(terminal, "failure-details", result)
        assert len(server.records) == 1, "Viewing recovery details retried speech"
        terminal.send(b"\x1b")
        visible_failure((summary, "› Error details"))
        terminal.send(picker.END + b"\r")
        main_form()
        visible_failure(("› More actions…",))
        assert terminal.child.poll() is None, "Back to form quit the application"
        assert len(server.records) == 1, "Back to form retried speech"
        mark(terminal, "failure-returned-form", result)

        terminal.send(picker.HOME + b"\r")
        wait(terminal, "gpt-4o-mini-tts-2025-03-20")
        terminal.send(picker.DOWN + picker.UP + b"\r")
        main_form()
        terminal.send(picker.END + b"\r" + picker.END + picker.UP + b"\r")
        visible_failure((summary, "› API setup"))  # More -> Last error.
        terminal.send(b"\x1b")
        visible_failure(("More actions", "› Last error"))
        terminal.send(b"\x1b")
        main_form()
        visible_failure(("› More actions…",))
        mark(terminal, "failure-settings-no-retry", result)
        assert len(server.records) == 1, "Navigation or Last error retried speech"
        assert json.loads(public(["--format", "json", "audio", "history"])) == []
        result["requests_before_explicit_retry"] = len(server.records)

        terminal.send(b"\x07")
        visible_failure(("Duration 00:02.000", "Play audio"),
                        absent=(summary, protocol_summary, "HTTP status:", fixture.FAILURE_REQUEST_ID))
        mark(terminal, "failure-recovered-result", result)
        assert len(server.records) == 2, "Explicit retry did not produce exactly one new request"
        assert server.records[1]["body"] == failed_request, "Recovery changed the retained draft or settings"
        assert server.records[1].get("response_status", 200) == 200
        assert server.records[1]["response_sha256"] == hashlib.sha256(fixture.WAV).hexdigest()
        entries = json.loads(public(["--format", "json", "audio", "history"]))
        assert len(entries) == 1 and not entries[0]["settings"].get("preview", False)
        entry = entries[0]
        assert entry["settings"]["input"] == fixture.TEXT
        assert entry["settings"]["instructions"] == fixture.DEFAULT_INSTRUCTIONS
        inspection = json.loads(public(["--format", "json", "audio", "inspect", entry["id"]]))
        assert inspection["analysis"]["duration"] == 2
        original = public(["audio", "export", entry["id"], "--output", "-"])
        assert original == fixture.WAV, "Successful retry did not retain original WAV bytes"
        (args.output / "failure-inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
        (args.output / "failure-recovered.wav").write_bytes(original)
        terminal.send(b"\x1b[Z" + picker.END + b"\r")
        visible_failure(("More actions", "Back to form"), absent=("Last error",))
        terminal.send(b"\x1b")
        main_form()
        visible_failure(("› More actions…",))
        terminal.send(b"\t")
        visible_failure(("Duration 00:02.000", "› Play audio"))
        result["failure_checks"] = {
            "http_status": 401, "request_id": fixture.FAILURE_REQUEST_ID,
            "safe_details_visible": True, "untrusted_message_canaries_absent": True,
            "escape_returned_without_retry": True, "navigation_did_not_retry": True,
            "default_instructions_sent": True, "draft_preserved": True,
            "explicit_retry_succeeded": True, "obsolete_feedback_cleared": True,
            "retained_results": len(entries), "original_audio_sha256": hashlib.sha256(original).hexdigest(),
        }
        terminal.send(b"\x03")
        terminal.finish(130)
        safe_screen()
        assert len(server.records) == 2, "Cancellation retried speech"

        # A second session proves legacy Instructions omission and the two-step
        # recovery/form Escape path without adding a fourth request.
        server.queue_auth_failure()
        failed_terminal = None
        try:
            width, height = terminal.initial_size
            failed_terminal = Terminal(str(args.binary), ["audio", "speak"], env, width=width, height=height)
            wait(failed_terminal, "Text to speech")
            failed_terminal.send(b"\t")
            wait(failed_terminal, ("╭ Instructions ", "› Instructions"))
            visible_failure((fixture.DEFAULT_INSTRUCTIONS,), target=failed_terminal)
            failed_terminal.send(b"\x1b[Z")
            paste(failed_terminal, fixture.TEXT)
            failed_terminal.send(b"\t\t\r" + picker.DOWN * 3 + b"\r")
            visible_failure(("tts-1", "Not used by this model"), target=failed_terminal)
            failed_terminal.send(picker.DOWN + b"\r")
            wait(failed_terminal, "alloy")
            failed_terminal.send(b"\r")
            visible_failure(("Alloy",), target=failed_terminal)
            failed_terminal.send(picker.UP + b"\r" + picker.UP * 3 + b"\r")
            failed_terminal.send(b"\x1b[Z")
            wait(failed_terminal, ("╭ Instructions ", "› Instructions"))
            visible_failure((fixture.DEFAULT_INSTRUCTIONS,), target=failed_terminal)
            failed_terminal.send(b"\t\r" + picker.DOWN * 3 + b"\r")
            visible_failure(("Not used by this model", "Alloy"), target=failed_terminal)
            assert len(server.records) == 2, "Legacy model selection submitted speech"
            mark(failed_terminal, "failure-legacy-ready", result)
            failed_terminal.send(b"\x07")
            visible_failure((summary, "API setup", "Back to form"), target=failed_terminal)
            mark(failed_terminal, "failure-error-exit-recovery", result)
            assert len(server.records) == 3 and server.records[2].get("response_status") == 401
            legacy = server.records[2]["body"]
            assert legacy["model"] == "tts-1" and legacy["voice"] == "alloy"
            assert legacy["input"] == fixture.TEXT and "instructions" not in legacy
            failed_terminal.send(b"\x1b")
            main_form(failed_terminal)
            visible_failure(("› More actions…",), target=failed_terminal)
            assert failed_terminal.child.poll() is None, "First Escape bypassed the recovery return"
            assert len(server.records) == 3, "Recovery Escape retried speech"
            mark(failed_terminal, "failure-error-exit-form", result)
            failed_terminal.send(b"\x1b")
            failed_terminal.finish(1)
            check_frame_boundaries(failed_terminal)
            safe_screen(failed_terminal)
            restored = bytes(failed_terminal.raw).rsplit(b"\x1b[?2004l", 1)
            assert len(restored) == 2, "Error exit omitted terminal restoration"
            diagnostic = picker.ANSI.sub("", restored[1].decode("utf-8", errors="strict"))
            assert protocol_summary in diagnostic, "Error exit omitted its HTTP status after terminal restoration"
            assert "Request ID: " + fixture.FAILURE_REQUEST_ID in diagnostic
            assert "Check API key, organization and project." in diagnostic
            assert "Key setup:" in diagnostic and "help setup" in diagnostic
            mark(failed_terminal, "failure-error-exit-summary", result)
            observer = failed_terminal.navigation_observer
            retained = observer.scrollback_lines()
            visible = observer.viewport_text()
            all_rows = visible + "\n" + "\n".join(line for lines in retained.values() for line in lines)
            assert "Text to speech" not in all_rows and summary not in all_rows, "Error exit retained owned form rows"
            assert not any(event[1] == "r" for event in failed_terminal.events)
            assert observer.provenance()["bytes_observed"] == len(failed_terminal.raw)
            failed_terminal.save(args.output / "failure-error-exit")
            (args.output / "failure-error-exit-final.txt").write_text(visible)
            (args.output / "failure-error-exit-scrollback.json").write_text(json.dumps(retained, ensure_ascii=False, indent=2) + "\n")
            assert len(server.records) == 3, "Escape from the failed form retried speech"
            assert json.loads(public(["--format", "json", "audio", "history"])) == entries
            result["failure_error_exit"] = {
                "exit_status": failed_terminal.child.returncode, "request_count": 1,
                "legacy_instructions_omitted": True, "instruction_draft_restored_on_supported_model": True,
                "recovery_escape_returned_before_exit": True,
                "safe_summary_after_terminal_restoration": True, "owned_form_rows_cleared": True,
                "final_diagnostic_allowed": True, "untrusted_message_canaries_absent": True,
                "history_preserved": True, "observer": observer.provenance(),
            }
        finally:
            if failed_terminal is not None:
                try:
                    failed_terminal.save(args.output / "failure-error-exit-latest")
                finally:
                    failed_terminal.close()
        result["failure_session_count"] = 2
        result["failure_total_request_count"] = len(server.records)
        assert result["failure_total_request_count"] == 3

    def check_native_modes(terminal):
        restored = termios.tcgetattr(terminal.slave)
        initial = list(terminal.initial)
        if sys.platform == "darwin":
            restored[3] &= ~termios.PENDIN
            initial[3] &= ~termios.PENDIN
        assert restored == initial, "CLI did not restore native terminal modes"

    def check_frame_boundaries(terminal):
        assert not re.search(rb"\x1b\[\?(?:47|1047|1049)[hl]", terminal.raw), "CLI used an alternate screen"
        assert not re.search(rb"\x1b\[[23]J", terminal.raw), "CLI cleared the screen or scrollback"

    def check_live_parent_terminal(terminal, form):
        # The CLI has exited, but its native parent still owns a live session.
        # Darwin can reject tcgetattr after that parent exits, even on our fd.
        check_native_modes(terminal)
        check_frame_boundaries(terminal)
        if form:
            assert terminal.raw.rfind(b"\x1b[?2004l") > terminal.raw.rfind(b"\x1b[?2004h") >= 0, "CLI did not restore bracketed paste"
            assert terminal.raw.rfind(b"\x1b[?25h") > terminal.raw.rfind(b"\x1b[?25l") >= 0, "CLI did not restore cursor visibility"
        else:
            assert b"\x1b[?2004h" not in terminal.raw, "Local command entered an interactive form"

    def process_snapshot():
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

    def check_children_exited(children, result):
        started = time.monotonic()
        pids = {child["pid"] for child in children}
        while True:
            remaining = [process for process in process_snapshot() if process["pid"] in pids]
            result["owned_child_cleanup"] = {
                "observed": children, "remaining": remaining, "checked_without_signals": True,
                "seconds": round(time.monotonic()-started, 4), "pass": bool(children) and not remaining,
            }
            if not remaining or time.monotonic()-started >= 2:
                break
            time.sleep(.03)
        assert children, "No owned helper was observed; cleanup coverage is missing"
        assert not remaining, "An observed CLI helper survived cancellation"

    def drain_after_exit(terminal, result):
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

    def seed_history(private, env):
        cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
        history = cache / "openai/audio"
        identity = "0123456789abcdef0123456789abcdef"
        artifact = history / identity
        artifact.mkdir(mode=0o700, parents=True)
        history.chmod(0o700)
        settings = {"input": fixture.TEXT, "instructions": fixture.INSTRUCTIONS,
                    "model": "gpt-4o-mini-tts", "voice": "coral", "format": "wav", "speed": 1}
        entry = {"id": identity, "created_at": "2026-10-09T00:00:00Z", "settings": settings,
                 "bytes": len(fixture.WAV), "sha256": hashlib.sha256(fixture.WAV).hexdigest()}
        (artifact / "audio").write_bytes(fixture.WAV)
        (artifact / "manifest.json").write_text(json.dumps({"version": 1, "entry": entry}) + "\n")
        for path in artifact.iterdir():
            path.chmod(0o600)
        return entry

    def navigation_case(terminal, server, result):
        up, down, ss3_up, ss3_down = b"\x1b[A", b"\x1b[B", b"\x1bOA", b"\x1bOB"
        backtab = b"\x1b[Z"
        focus = {"text": "› Text", "instructions": "› Instructions", "model": "› Model",
                 "more": "› More actions…", "generate": "› [ Generate speech ]", "history": "› History",
                 "play": "› Play audio", "save": "› Save audio…", "command": "Command · Bash/zsh"}
        result["navigation_checks"] = []

        def zero_requests():
            with server.lock:
                count = len(server.handler_threads)
            result["navigation_request_count"] = count
            assert count == 0 and not server.records and not server.failures, "Navigation reached the speech fixture"

        def check(name, target, keys=None, absent=()):
            if keys is not None:
                terminal.send(keys)
            drain(terminal, .15)
            wait(terminal, focus.get(target, target), absent=absent)
            zero_requests()
            result["navigation_checks"].append({"name": name, "focus": target})

        def open_more(name):
            check(name + " select More", "more", picker.END)
            check(name + " open More", "history", b"\r")

        mark(terminal, result["case"] + "-initial", result)
        check("empty text CSI Down", "instructions", down)
        check("initial Instructions example", fixture.DEFAULT_INSTRUCTIONS)
        check("clear seeded Instructions for empty boundary", "instructions", b"\x01\x0b")
        check("empty instructions SS3 Down", "model", ss3_down)
        check("Model CSI Up", "instructions", up)
        check("empty instructions SS3 Up", "text", ss3_up)
        check("Tab Text to Instructions", "instructions", b"\t")
        check("Tab Instructions to Settings", "model", b"\t")
        check("Tab Settings wraps to Text without result", "text", b"\t")
        check("reverse Tab Text to Settings", "model", backtab)
        check("reverse Tab Settings to Instructions", "instructions", backtab)
        check("reverse Tab Instructions to Text", "text", backtab)
        check("open settings", "model", b"\t\t")
        check("open model chooser", "Enter select · Esc back", b"\r")
        check("CSI Down remains in chooser", "Enter select · Esc back", down)
        check("SS3 Up remains in chooser", "Enter select · Esc back", ss3_up)
        check("Tab remains in chooser", "Enter select · Esc back", b"\t")
        check("Escape returns to Model", "model", b"\x1b")
        check("End selects More", "more", picker.END)
        check("More Down stays bounded without result", "more", down)
        check("More Up selects Generate", "generate", up)
        check("Generate Down selects More", "more", ss3_down)
        check("Tab More to Text for long command", "text", b"\t")
        paste(terminal, "Command inspection sample. " * 180)
        check("long command draft stays local", "text")
        check("return to More for command inspection", "more", b"\t\t")
        check("open More command route", "history", b"\r")
        check("select Show command", "› Show command", down * 3)
        check("open Command information", "command", b"\r")
        check("Command starts with printf", "printf")
        initial_command = screen(terminal)
        check("Command End scrolls", "command", picker.END)
        assert screen(terminal) != initial_command, "Command modal did not scroll through its complete command"
        check("Command Down stays at bottom", "command", down * 4)
        check("Command Home returns to first rows", "printf", picker.HOME)
        check("Command Up stays in its modal", "command", up * 4)
        check("Command Escape returns to More", "› Show command", b"\x1b")
        check("More Home selects History", "history", picker.HOME)
        check("More Help is selectable", "› Help", down * 4)
        check("Help opens locally", "Use Tab to move between sections.", b"\r")
        check("Help End remains local", "Help", picker.END)
        check("Help Escape returns to More", "› Help", b"\x1b")
        check("More End selects Back", "› Back to form", picker.END)
        check("More Down stays at last action", "› Back to form", down)
        check("Back returns to Settings More", "more", b"\r")
        check("open More to clear long command draft", "history", b"\r")
        check("select Clear for long command draft", "› Clear form", down * 2)
        check("clear long command draft locally", "more", b"\r")
        check("Home selects Model", "model", picker.HOME)
        check("Model Up enters Instructions", "instructions", up)
        check("Instructions Up enters Text", "text", up)

        editor_columns = terminal.initial_size[0] - 8
        result["exact_width_editor_columns"] = editor_columns
        for editor in ("text", "instructions"):
            for name, value in (("single-line", "Boundary"), ("multiline", "First line\nSecond line\nThird line"),
                                ("wrapped-unicode", "界🙂e\u0301 " * 26), ("exact-width-unicode", "界" * editor_columns)):
                prefix = editor + " " + name
                if editor == "instructions":
                    check(prefix + " enter editor", "instructions", down)
                paste(terminal, value)
                check(prefix + " paste keeps editor", editor)
                if name == "exact-width-unicode":
                    # Two complete visual rows leave the editor's padding row.
                    check(prefix + " End reaches padding row", editor, picker.END)
                if name == "single-line":
                    for label, keys in (("Shift Down", b"\x1b[1;2B"), ("Alt Down", b"\x1b[1;3B"),
                                        ("Ctrl Down", b"\x1b[1;5B"), ("selection Down", b"\x1b[1;2D" + down)):
                        check(editor + " " + label + " remains editing", editor, keys)
                        check(editor + " " + label + " deselect", editor, b"\x1b[C")
                    check(editor + " Alt Enter inserts a newline", editor, b"\x1b\r")
                    paste(terminal, "Second")
                check(prefix + " Up stays within visual rows", editor, ss3_up)
                row = "padding row" if name == "exact-width-unicode" else "last visual row"
                check(prefix + " Down returns to " + row, editor, down)
                if name == "wrapped-unicode" and editor == "text":
                    mark(terminal, result["case"] + "-wrapped-unicode", result)
                check(prefix + " last-row Down leaves editor", "instructions" if editor == "text" else "model", ss3_down)
                if editor == "text":
                    check(prefix + " Tab returns to Settings", "model", b"\t")
                open_more(prefix + " clear")
                check(prefix + " select Clear", "› Clear form", down * 2)
                check(prefix + " Clear returns to More setting", "more", b"\r")
                visible = screen(terminal)
                assert value.splitlines()[0] not in visible and fixture.DEFAULT_INSTRUCTIONS not in visible
                check(prefix + " return to Model", "model", picker.HOME)
                check(prefix + " return to Instructions", "instructions", up)
                check(prefix + " return to Text", "text", up)

        check("select Settings for History", "model", b"\t\t")
        open_more("History")
        check("open local History", "Enter select · Esc back", b"\r")
        check("restore local result", "play", b"\r")
        wait(terminal, "Duration 00:02.000")
        mark(terminal, result["case"] + "-restored-result", result)
        check("reverse Tab Result to Settings", "more", backtab)
        check("More Down enters Play", "play", down)
        check("Result Down selects Save", "save", ss3_down)
        check("Result Down stays on Save", "save", down)
        check("Result Up returns to Play", "play", up)
        check("Result Up enters Settings More", "more", ss3_up)
        check("Tab Settings to Result", "play", b"\t")
        check("Result End selects Save", "save", picker.END)
        check("Result Home selects Play", "play", picker.HOME)
        check("Tab Result wraps to Text", "text", b"\t")
        check("reverse Tab Text returns to Result", "play", backtab)
        check("Tab Result wraps again", "text", b"\t")
        check("Tab Text to Instructions with result", "instructions", b"\t")
        check("Tab Instructions to Settings with result", "more", b"\t")
        check("Tab Settings to Result again", "play", b"\t")
        check("reverse Tab Result to Settings again", "more", backtab)
        visible = screen(terminal)
        assert "Command preview" not in visible and "printf" not in visible, "Main form retained advanced command content"
        mark(terminal, result["case"] + "-complete", result)
        result["navigation_check_count"] = len(result["navigation_checks"])
        assert result["navigation_check_count"] == 185, "Navigation inventory changed"
        zero_requests()
        terminal.send(b"\x03")
        terminal.finish(130)
        zero_requests()

    def startup_case(private, env, programs, server, result):
        startup_env = dict(env)
        startup_env.pop("OPENAI_PICKER_SHELL", None)
        zdotdir = private / "zsh"
        zdotdir.mkdir(mode=0o700)
        startup_env["ZDOTDIR"] = str(zdotdir)
        config = Path(env["XDG_CONFIG_HOME"])
        config.mkdir(mode=0o700)
        canaries = [private / name for name in (".zshrc", ".bashrc", ".bash_profile")]
        canaries.append(zdotdir / ".zshrc")
        for canary in canaries:
            canary.write_text("# Synthetic audio startup canary. Preserve these bytes.\n")
            canary.chmod(0o600)
        on_path = programs / "openai"
        on_path.symlink_to(args.binary.resolve())
        assert os.path.samefile(on_path, args.binary), "PATH selected a different executable"
        startup_env["AUDIO_EXPECTED_COMMAND"] = str(on_path)
        cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
        entry = seed_history(private, env)
        identity = entry["id"]

        def files(directory):
            snapshot = []
            for path in sorted(directory.rglob("*")):
                kind = "directory" if path.is_dir() else "file"
                assert not path.is_symlink(), "Startup created an unexpected symlink"
                digest = None if path.is_dir() else hashlib.sha256(path.read_bytes()).hexdigest()
                snapshot.append((str(path.relative_to(directory)), kind, path.stat().st_mode & 0o777, digest))
            return snapshot

        expected_profiles = {str(path): path.read_bytes() for path in canaries}
        expected_config, expected_cache = files(config), files(cache)
        parent_script = ('[ "$(command -v openai)" = "$AUDIO_EXPECTED_COMMAND" ] || exit 90\n'
                         'openai "$@"\naudio_status=$?\n'
                         'printf "\\n__AUDIO_STARTUP_STATUS__=%s\\n" "$audio_status"\n'
                         'IFS= read -r audio_release\nexit "$audio_status"\n')
        result["coverage_limit"] = "Fake HOME reaches the later real-home guard; no-write coverage does not prove early eligibility exclusion."
        result["commands"], result["shells_not_run"] = [], []
        for name, path, options in (("bash", "/bin/bash", ["--noprofile", "--norc"]), ("zsh", "/bin/zsh", ["-f"])):
            if not Path(path).is_file():
                result["shells_not_run"].append(name)
                continue

            def native(arguments, label, form=False):
                command_env = dict(startup_env, SHELL=path)
                if not form:
                    command_env.update(OPENAI_BASE_URL="://synthetic-private-endpoint",
                                       OPENAI_MTLS_CLIENT_CERT_FILE=str(private / "missing-cert"))
                child = Terminal(path, [*options, "-c", parent_script, "audio-startup", *arguments], command_env,
                                 width=90, height=34)
                try:
                    if form:
                        wait(child, "Text to speech")
                        child.send(b"\x03")
                    child.wait("__AUDIO_STARTUP_STATUS__=", timeout=12)
                    statuses = re.findall(r"__AUDIO_STARTUP_STATUS__=(\d+)", child.text())
                    assert statuses, "Native parent did not report the CLI status"
                    code = int(statuses[-1])
                    assert code == (130 if form else 0), (name, label, code)
                    assert child.child.poll() is None, "Native parent exited before its release handshake"
                    check_live_parent_terminal(child, form)
                    output = child.text().split("__AUDIO_STARTUP_STATUS__=", 1)[0].strip()
                    child.send(b"release-parent\n")
                    deadline = time.monotonic() + 12
                    while child.child.poll() is None:
                        child.read(.03)
                        if time.monotonic() >= deadline:
                            raise AssertionError("Native parent did not exit after its release handshake")
                    child.child.wait(timeout=1)
                    for _ in range(3):
                        child.read(.03)
                    assert child.child.returncode == code, "Native parent changed the CLI exit status"
                    child.save(args.output / ("startup-" + name + "-" + label))
                    result["commands"].append({"shell": name, "case": label, "exit_status": code,
                                               "native_parent_alive_after_cli": True,
                                               "native_modes_checked_before_release": True,
                                               "ansi_modes_checked_before_release": True,
                                               "native_parent_exit_status": child.child.returncode})
                    return output
                finally:
                    child.close()

            native(["audio", "speak"], "form", form=True)
            for position in ("before", "group", "leaf"):
                for command in ("history", "inspect", "export", "play"):
                    leaf = [command] + ([] if command == "history" else [identity])
                    destination = private / ("startup-export-" + name + "-" + position + ".wav")
                    if command == "export":
                        leaf += ["--output", str(destination)]
                    argv = (["--format", "json", "audio", *leaf] if position == "before" else
                            ["audio", "--format", "json", *leaf] if position == "group" else
                            ["audio", *leaf, "--format", "json"])
                    output = native(argv, command + "-" + position)
                    if command == "history":
                        assert json.loads(output) == [entry], "Startup changed history machine output"
                    elif command == "inspect":
                        assert json.loads(output)["analysis"]["duration"] == 2
                    else:
                        assert output == "", "Startup decorated a local operation"
                        if command == "export":
                            assert destination.read_bytes() == fixture.WAV
                    assert {str(p): p.read_bytes() for p in canaries} == expected_profiles, "Startup changed a profile canary"
                    assert files(config) == expected_config, "Startup wrote optional shell configuration"
                    assert files(cache) == expected_cache, "Startup changed existing private history"
            assert not server.records, "Local startup coverage reached the synthetic API"
        assert result["commands"], "No supported native shell was available"
        result.update(pass_=True, exit_status=0, requests=[])
        result["pass"] = result.pop("pass_")

    cases = ("workflow", "compact", "navigation", "navigation-compact", "failure", "failure-compact",
             "ctrl-c", "sigterm", "busy-ctrl-c", "busy-sigterm",
             "stopped-reader", "startup") if args.case == "all" else (args.case,)
    with harness_lifecycle():
        for case in cases:
            result = {"case": case, "states": [], "pass": False}
            report["cases"].append(result)
            with tempfile.TemporaryDirectory(prefix="audio-workbench-demo-") as temporary:
                private = Path(temporary)
                programs = private / "bin"
                programs.mkdir(mode=0o700)
                playback = private / "playback.json"
                # The fake player receives actual generated bytes. It emits no sound.
                helper = ("#!" + sys.executable + "\nimport hashlib,json,os,sys,time\n"
                          "assert 'OPENAI_API_KEY' not in os.environ and 'FFREPORT' not in os.environ\n"
                          "data=sys.stdin.buffer.read()\n"
                          "with open(" + repr(str(playback)) + ", 'a') as out:\n"
                          " out.write(json.dumps({'pid':os.getpid(),'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()})+'\\n')\n"
                          "time.sleep(2)\n")
                (programs / "ffplay").write_text(helper)
                (programs / "ffplay").chmod(0o700)
                busy = case in ("busy-ctrl-c", "busy-sigterm")
                failure = case in failure_cases
                with protect_ownership():
                    server = OwnedSpeechFixture(partial=busy, auth_failure=failure)
                    server.timeout = .1
                    stopped = threading.Event()
                    worker = threading.Thread(target=serve_fixture, args=(server, stopped))
                    server_state = {"server": server, "worker": worker, "stopped": stopped,
                                    "result": result, "closed": False}
                    owned_servers.append(server_state)
                    worker.start()
                env = {"PATH": str(programs) + ":/usr/bin:/bin", "HOME": temporary,
                       "XDG_CACHE_HOME": str(private / "cache"), "XDG_CONFIG_HOME": str(private / "config"),
                       "TERM": "xterm-256color", "LANG": "en_US.UTF-8", "GOMAXPROCS": "2",
                       "OPENAI_PICKER_SHELL": "off", "OPENAI_API_KEY": "synthetic-demo-key",
                       "OPENAI_BASE_URL": server.endpoint, "FORCE_COLOR": "1",
                       "COLORTERM": "truecolor", "COLORFGBG": "0;15" if args.theme == "light" else "15;0"}
                if args.theme == "no-color":
                    env.update(NO_COLOR="1", FORCE_COLOR="0")
                navigation = case in ("navigation", "navigation-compact")
                if navigation:
                    env.update(NO_COLOR="1", FORCE_COLOR="0")
                    result["effective_theme"] = "no-color"
                terminal = None

                def public(arguments):
                    command = subprocess.run([str(args.binary), *arguments], env=env, stdin=subprocess.DEVNULL,
                                             capture_output=True, timeout=12)
                    assert command.returncode == 0 and command.stderr == b"", "Local audio command failed"
                    return command.stdout

                try:
                    if case == "startup":
                        startup_case(private, env, programs, server, result)
                        continue
                    if navigation:
                        seeded_entry = seed_history(private, env)
                    width, height = (40, 12) if case in ("compact", "navigation-compact", "failure-compact") else (90, 34)
                    terminal = Terminal(str(args.binary), ["audio", "speak"], env, width=width, height=height)
                    result["initial_size"] = [width, height]
                    wait(terminal, "Text to speech")
                    if case == "workflow":
                        result["resize_checkpoints"] = not args.no_resize
                        mark(terminal, "initial-form", result)
                        paste(terminal, fixture.TEXT)
                        terminal.send(b"\t")
                        wait(terminal, ("╭ Instructions ", "› Instructions"))
                        terminal.send(b"\x01\x0b")
                        paste(terminal, fixture.INSTRUCTIONS)
                        terminal.send(b"\t\r")
                        wait(terminal, "gpt-4o-mini-tts-2025-12-15")
                        terminal.send(picker.DOWN * 2)
                        mark(terminal, "model-choices", result)
                        terminal.send(b"\r")
                        wait(terminal, "Model          gpt-4o-mini-tts-2025-12-15")
                        terminal.send(picker.DOWN + b"\r")
                        wait(terminal, "Enter select · Esc back")
                        terminal.send(picker.UP * 3)
                        mark(terminal, "voice-choices", result)
                        terminal.send(b"\r")
                        wait(terminal, "Voice          Ash")
                        terminal.send(picker.DOWN + b"\r")
                        wait(terminal, "Response format")
                        mark(terminal, "format-choices", result)
                        terminal.send(picker.DOWN + b"\r")
                        wait(terminal, "Format         PCM")
                        terminal.send(b"\r" + picker.UP + b"\r")
                        wait(terminal, "Format         WAV")
                        terminal.send(picker.DOWN + b"\x1b[C")
                        wait(terminal, "Speed          1.25 x")
                        mark(terminal, "selected-settings", result)
                        assert not server.records, "Choosing or typing generated speech"
                        terminal.send(b"\x07")
                        wait(terminal, "Duration 00:02.000")
                        mark(terminal, "generated-result", result)
                        assert len(server.records) == 1, "Generation request count changed"
                        request = server.records[0]["body"]
                        assert request["input"] == fixture.TEXT and request["instructions"] == fixture.INSTRUCTIONS
                        assert (request["model"], request["voice"], request["response_format"], request["speed"]) == (
                            "gpt-4o-mini-tts-2025-12-15", "ash", "wav", 1.25)
                        entries = json.loads(public(["--format", "json", "audio", "history"]))
                        assert len(entries) == 1 and not entries[0]["settings"].get("preview", False)
                        entry = entries[0]
                        inspection = json.loads(public(["--format", "json", "audio", "inspect", entry["id"]]))
                        assert inspection["analysis"]["duration"] == 2
                        peaks = inspection["analysis"]["peaks"]
                        assert len(peaks) > 8 and max(peaks)-min(peaks) > .5, "Waveform did not use varying samples"
                        (args.output / "inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
                        terminal.send(b" ")
                        wait(terminal, "Stop audio")
                        deadline = time.monotonic() + 3
                        while not playback.exists() and time.monotonic() < deadline:
                            drain(terminal, .05)
                        assert playback.exists(), "The fake player never received generated audio"
                        played = json.loads(playback.read_text().splitlines()[0])
                        assert played["sha256"] == hashlib.sha256(fixture.WAV).hexdigest()
                        terminal.send(b" ")
                        wait(terminal, "Play audio")
                        terminal.send(b"s")
                        wait(terminal, "Save original audio to:")
                        terminal.send(b"\x01\x0b-\r")
                        wait(terminal, "Enter a file path for saving.")
                        assert len(server.records) == 1, "Invalid save path generated speech"
                        mark(terminal, "save-validation", result)
                        destination = private / "saved speech.wav"
                        terminal.send(b"\x01\x0b")
                        paste(terminal, str(destination))
                        terminal.send(b"\r")
                        wait(terminal, "Saved:")
                        assert destination.read_bytes() == fixture.WAV
                        (args.output / "exported.wav").write_bytes(destination.read_bytes())
                        mark(terminal, "saved-result", result)
                        terminal.send(b"\x1b[Z" + picker.END + b"\r")
                        wait(terminal, "› History")
                        terminal.send(b"\r")
                        wait(terminal, entry["id"])
                        mark(terminal, "history", result)
                        terminal.send(b"\r")
                        wait(terminal, "Duration 00:02.000")
                        wait(terminal, "Play audio", absent=("Opening audio",))
                        mark(terminal, "restored-result", result)
                        if not args.no_resize:
                            terminal.resize(40, 12)
                            wait(terminal, "00:02.000")
                            mark(terminal, "compact-40x12", result)
                            terminal.resize(28, 8)
                            wait(terminal, "Resize to 40 x 12.")
                            terminal.send(b"\x07discarded")
                            drain(terminal)
                            assert len(server.records) == 1, "Small terminal accepted a hidden submission"
                            mark(terminal, "too-small", result)
                            terminal.resize(90, 34)
                            wait(terminal, "Duration 00:02.000")
                        terminal.send(b"\x1b")
                        wait(terminal, ("╭ Text ", "› Text"))
                        terminal.send(b"\t\t" + picker.HOME + picker.DOWN + b"\r")
                        wait(terminal, "Enter select · Esc back")
                        terminal.send(picker.DOWN + b"\r")
                        wait(terminal, "Voice          Ballad")
                        assert len(server.records) == 1, "Selecting an audition voice generated speech"
                        terminal.send(picker.END + b"\r")
                        wait(terminal, "› History")
                        terminal.send(picker.DOWN + b"\r")
                        wait(terminal, "Result: Ballad")
                        mark(terminal, "voice-audition", result)
                        assert len(server.records) == 2 and server.records[1]["body"]["input"] == fixture.AUDITION
                        assert json.loads(public(["--format", "json", "audio", "history"])) == entries
                        terminal.send(b"\x1b")
                        wait(terminal, "Result: Ash")
                        terminal.send(b"\t\t" + picker.END + b"\r")
                        wait(terminal, "› History")
                        terminal.send(picker.DOWN * 2)
                        wait(terminal, "› Clear form")
                        terminal.send(b"\r")
                        wait(terminal, "› More actions…", absent=("Result:", fixture.TEXT.splitlines()[0], fixture.INSTRUCTIONS,
                                                                    fixture.DEFAULT_INSTRUCTIONS))
                        mark(terminal, "cleared-form", result)
                        assert json.loads(public(["--format", "json", "audio", "history"])) == entries
                        assert destination.read_bytes() == fixture.WAV and len(server.records) == 2
                        terminal.send(b"\x03")
                        terminal.finish(130)
                    elif failure:
                        failure_case(terminal, server, result, public, env)
                    elif case == "compact":
                        mark(terminal, "compact-initial-form", result)
                        paste(terminal, fixture.TEXT)
                        assert not server.records, "Typing generated speech"
                        terminal.send(b"\x07")
                        wait(terminal, "Duration 00:02.000")
                        assert len(server.records) == 1, "Compact generation request count changed"
                        request = server.records[0]["body"]
                        assert request["input"] == fixture.TEXT and request.get("instructions", "") == ""
                        assert (request["model"], request["voice"], request["response_format"], request.get("speed", 1)) == (
                            "gpt-4o-mini-tts", "coral", "wav", 1)
                        entries = json.loads(public(["--format", "json", "audio", "history"]))
                        assert len(entries) == 1 and not entries[0]["settings"].get("preview", False)
                        entry = entries[0]
                        inspection = json.loads(public(["--format", "json", "audio", "inspect", entry["id"]]))
                        assert inspection["analysis"]["duration"] == 2
                        peaks = inspection["analysis"]["peaks"]
                        assert len(peaks) > 8 and max(peaks)-min(peaks) > .5, "Compact waveform did not use varying samples"
                        original = public(["audio", "export", entry["id"], "--output", "-"])
                        assert original == fixture.WAV, "Compact export changed original audio bytes"
                        (args.output / "compact-exported.wav").write_bytes(original)
                        (args.output / "compact-inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
                        mark(terminal, "compact-40x12", result)
                        assert not any(event[1] == "r" for event in terminal.events), "Fixed-size capture contains a resize"
                        result["fixed_geometry"] = True
                        result["original_audio_sha256"] = hashlib.sha256(original).hexdigest()
                        terminal.send(b"\x03")
                        terminal.finish(130)
                        assert json.loads(public(["--format", "json", "audio", "history"])) == entries
                        assert len(server.records) == 1, "Compact cancellation generated speech"
                    elif navigation:
                        navigation_case(terminal, server, result)
                        assert json.loads(public(["--format", "json", "audio", "history"])) == [seeded_entry]
                        result["seeded_history_preserved"] = True
                    elif busy:
                        paste(terminal, fixture.TEXT)
                        assert not server.records, "Typing generated speech"
                        terminal.send(b"\x07")
                        deadline = time.monotonic() + 12
                        while not server.partial_ready.is_set():
                            terminal.read(.03)
                            assert terminal.child.poll() is None, "Audio form exited before the partial response"
                            assert time.monotonic() < deadline, "Fixture did not flush the partial response"
                        assert len(server.records) == 1, "Busy cancellation must receive exactly one request"
                        assert 0 < server.partial_bytes < server.records[0]["response_bytes"]
                        assert not server.partial_release.is_set(), "Fixture released the response before cancellation"
                        result["partial_response"] = {"ready_before_signal": True, "delivered_bytes": server.partial_bytes,
                                                      "total_bytes": server.records[0]["response_bytes"],
                                                      "peer_closed_before_release": False, "handler_finished": False}
                        started = time.monotonic()
                        if case == "busy-ctrl-c":
                            terminal.send(b"\x03")
                        else:
                            os.kill(terminal.child.pid, signal.SIGTERM)
                        terminal.finish(130 if case == "busy-ctrl-c" else 143, timeout=5)
                        result["busy_cancel_seconds"] = round(time.monotonic()-started, 4)
                        assert time.monotonic()-started < 5, "Busy cancellation exceeded five seconds"
                        assert server.partial_closed.wait(2), "Cancellation did not close the partial response stream"
                        assert not server.partial_release.is_set(), "Cleanup released the response before observing cancellation"
                        result["partial_response"]["peer_closed_before_release"] = True
                        assert server.partial_done.wait(2), "Partial response handler did not finish after cancellation"
                        result["partial_response"]["handler_finished"] = True
                        assert json.loads(public(["--format", "json", "audio", "history"])) == [], "Partial audio entered history"
                        cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
                        history = cache / "openai/audio"
                        artifacts = sorted(str(path.relative_to(history)) for path in history.rglob("*"))
                        result["partial_response"]["remaining_history_artifacts"] = artifacts
                        assert not artifacts, "Cancellation retained partial history artifacts"
                        assert len(server.records) == 1, "Cancellation retried generation"
                        result["partial_response"]["history_entries"] = 0
                    elif case == "ctrl-c":
                        terminal.send(b"\x03")
                        terminal.finish(130)
                    elif case == "sigterm":
                        os.kill(terminal.child.pid, signal.SIGTERM)
                        terminal.finish(143)
                    else:
                        children = [process for process in process_snapshot() if process["ppid"] == terminal.child.pid]
                        worker_names = {args.binary.name, args.binary.resolve().name}
                        observed_worker = any(Path(child["comm"]).name in worker_names for child in children)
                        result["owned_children_before_blocking"] = children
                        result["terminal_worker_observed"] = observed_worker
                        assert observed_worker, "No self-executable terminal helper was observed; cleanup coverage is missing"
                        # Deliberately stop consuming stdout while triggering redraws.
                        # Only the owned PTY and child receive these resize signals.
                        for index in range(80):
                            if terminal.child.poll() is not None:
                                raise AssertionError("Audio form failed before cancellation")
                            terminal.resize(180 + index % 2, 60)
                            time.sleep(.02)
                        started = time.monotonic()
                        os.kill(terminal.child.pid, signal.SIGINT)
                        terminal.child.wait(timeout=5)  # No reads release terminal backpressure.
                        result["stopped_reader_cancel_seconds"] = round(time.monotonic()-started, 4)
                        result["exit_status"] = terminal.child.returncode
                        assert terminal.child.returncode == 130, "Stopped-reader cancellation changed exit status"
                        check_native_modes(terminal)
                        result["native_modes_restored"] = True
                        check_children_exited(children, result)
                        drain_after_exit(terminal, result)
                        check_frame_boundaries(terminal)
                        paste_on, paste_off = terminal.raw.rfind(b"\x1b[?2004h"), terminal.raw.rfind(b"\x1b[?2004l")
                        cursor_off, cursor_on = terminal.raw.rfind(b"\x1b[?25l"), terminal.raw.rfind(b"\x1b[?25h")
                        result["ansi_cleanup"] = {
                            "paste_on_offset": paste_on, "paste_off_offset": paste_off,
                            "cursor_off_offset": cursor_off, "cursor_on_offset": cursor_on,
                            "bracketed_paste_restored": paste_off > paste_on >= 0,
                            "cursor_visibility_restored": cursor_on > cursor_off >= 0,
                            "scope": "Observed only. Blocked output can prevent bounded visual cleanup.",
                        }
                    terminal.save(args.output / case)
                    final = screen(terminal)
                    (args.output / (case + "-final.txt")).write_text(final)
                    if navigation or failure:
                        observer = terminal.navigation_observer
                        retained = observer.scrollback_lines()
                        (args.output / (case + "-scrollback.json")).write_text(json.dumps(retained, ensure_ascii=False, indent=2) + "\n")
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
                    if case not in ("workflow", "compact", *failure_cases) and not busy:
                        assert not server.records, "Cancellation generated speech"
                    result.update(exit_status=terminal.child.returncode, requests=server.records, pass_=True)
                    result["pass"] = result.pop("pass_")
                finally:
                    try:
                        if terminal is not None:
                            try:
                                if terminal.child.poll() is not None:
                                    result["exit_status"] = terminal.child.returncode
                                terminal.save(args.output / (case + "-latest"))
                            finally:
                                terminal.close()
                    finally:
                        try:
                            try:
                                # Launch can be interrupted before the caller
                                # receives the registered Terminal instance.
                                with protect_ownership():
                                    for owned in owned_terminals:
                                        if owned.case_result is result:
                                            owned.close()
                            finally:
                                close_fixture(server_state)
                        finally:
                            (args.output / "results.json").write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n")
    if not args.emit:
        print(json.dumps({"passed": len(report["cases"]), "source": args.source, "binary_sha256": report["binary_sha256"]}))
    return 130 if args.emit else 0


if __name__ == "__main__":
    raise SystemExit(main())
