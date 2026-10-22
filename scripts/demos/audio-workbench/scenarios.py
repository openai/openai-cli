"""Direct audio workbench scenarios; CaseContext owns their resources and capture state."""

import hashlib
import json
import os
from pathlib import Path
import re
import signal
import struct
import sys
import termios
import time


def failure_case(ctx):
    terminal = ctx.terminal
    server = ctx.server
    result = ctx.result
    public = ctx.public
    env = ctx.env
    summary = "The API did not accept this key."
    protocol_summary = "HTTP 401: Unauthorized."

    def safe_screen(target=terminal):
        visible = ctx.screen(target)
        for canary in ctx.fixture.FAILURE_CANARIES:
            assert canary.encode() not in target.raw, "Untrusted API message reached terminal output"
        assert b"synthetic-demo-key" not in target.raw, "Terminal disclosed the fake credential value"
        return " ".join(visible.split())

    def visible_failure(required, absent=(), timeout=12, target=terminal):
        deadline = time.monotonic() + timeout
        while True:
            ctx.drain(target, .06)
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
    ctx.wait(terminal, ("╭ Instructions ", "› Instructions"))
    visible_failure((ctx.fixture.DEFAULT_INSTRUCTIONS,))
    terminal.send(b"\x1b[Z")
    main_form()
    ctx.mark(terminal, "failure-initial-form", result)
    ctx.paste(terminal, ctx.fixture.TEXT)
    ctx.mark(terminal, "failure-ready", result)
    assert not server.records, "Editing submitted speech before explicit generation"
    terminal.send(b"\x07")
    visible_failure((summary, "› API setup", "Error details", "Back to form"),
                    absent=(protocol_summary, "HTTP status:", ctx.fixture.FAILURE_REQUEST_ID))
    ctx.mark(terminal, "failure-recovery", result)
    assert len(server.records) == 1 and server.records[0].get("response_status") == 401
    failed_request = server.records[0]["body"]
    assert failed_request["input"] == ctx.fixture.TEXT
    assert failed_request["instructions"] == ctx.fixture.DEFAULT_INSTRUCTIONS, "New draft omitted its actual Instructions example"
    assert json.loads(public(["--format", "json", "audio", "history"])) == [], "Failed speech entered History"

    # Inspect the actual in-form canonical guide. This action must neither
    # run setup steps nor mutate HOME, credentials, or request state.
    before_help = home_snapshot()
    terminal.send(b"\r")
    visible_failure(("API setup", "Close this form before following these steps.", "Then reopen audio speak in that shell."))
    ctx.mark(terminal, "failure-api-setup", result)
    guide_frames = [safe_screen()]
    for _ in range(32):
        terminal.send(b"\x1b[6~")
        ctx.drain(terminal, .08)
        current = safe_screen()
        guide_frames.append(current)
        if current == guide_frames[-2]:
            break
    else:
        raise AssertionError("API setup guide did not reach a bounded scroll endpoint")
    guide = " ".join(guide_frames)
    help_output = public(["help", "setup"])
    help_text = ctx.picker.ANSI.sub("", help_output.decode("utf-8", errors="strict"))
    for phrase in ("Set up your API key", "OPENAI_API_KEY", "This is a guide only."):
        assert phrase in guide and phrase in help_text, "API setup panel omitted canonical setup guidance"
    assert b"synthetic-demo-key" not in help_output
    assert all(canary.encode() not in help_output for canary in ctx.fixture.FAILURE_CANARIES)
    assert home_snapshot() == before_help, "Reading API setup changed synthetic HOME"
    assert len(server.records) == 1, "Reading API setup reached the speech API"
    (ctx.args.output / "failure-setup-help.txt").write_bytes(help_output)
    (ctx.args.output / "failure-setup-panel.json").write_text(json.dumps(guide_frames, ensure_ascii=False, indent=2) + "\n")
    result["setup_recovery"] = {"panel_title": "API setup", "canonical_panel_visible": True,
                                "close_and_reopen_guidance_visible": True,
                                "verification_arguments": [str(ctx.args.binary), "help", "setup"],
                                "exit_status": 0, "requests_added": 0, "synthetic_home_unchanged": True}
    terminal.send(b"\x1b")
    visible_failure((summary, "› API setup"))
    terminal.send(ctx.picker.DOWN + b"\r")
    visible_failure(("Error details",))
    terminal.send(ctx.picker.END)
    visible_failure(("Error details", "HTTP status: 401", "Request ID: " + ctx.fixture.FAILURE_REQUEST_ID, "Esc back"))
    ctx.mark(terminal, "failure-details", result)
    assert len(server.records) == 1, "Viewing recovery details retried speech"
    terminal.send(b"\x1b")
    visible_failure((summary, "› Error details"))
    terminal.send(ctx.picker.END + b"\r")
    main_form()
    visible_failure(("› More actions…",))
    assert terminal.child.poll() is None, "Back to form quit the application"
    assert len(server.records) == 1, "Back to form retried speech"
    ctx.mark(terminal, "failure-returned-form", result)

    terminal.send(ctx.picker.HOME + b"\r")
    ctx.wait(terminal, "gpt-4o-mini-tts-2025-03-20")
    terminal.send(ctx.picker.DOWN + ctx.picker.UP + b"\r")
    main_form()
    terminal.send(ctx.picker.END + b"\r" + ctx.picker.END + b"\r")
    visible_failure((summary, "› API setup"))  # More -> Last error.
    terminal.send(b"\x1b")
    visible_failure(("More actions", "› Last error"))
    terminal.send(b"\x1b")
    main_form()
    visible_failure(("› More actions…",))
    ctx.mark(terminal, "failure-settings-no-retry", result)
    assert len(server.records) == 1, "Navigation or Last error retried speech"
    assert json.loads(public(["--format", "json", "audio", "history"])) == []
    result["requests_before_explicit_retry"] = len(server.records)

    terminal.send(b"\x07")
    visible_failure(("00:00.000 / 00:02.000", "Play audio"),
                    absent=(summary, protocol_summary, "HTTP status:", ctx.fixture.FAILURE_REQUEST_ID))
    ctx.mark(terminal, "failure-recovered-result", result)
    assert len(server.records) == 2, "Explicit retry did not produce exactly one new request"
    assert server.records[1]["body"] == failed_request, "Recovery changed the retained draft or settings"
    assert server.records[1].get("response_status", 200) == 200
    assert server.records[1]["response_sha256"] == hashlib.sha256(ctx.fixture.WAV).hexdigest()
    entries = json.loads(public(["--format", "json", "audio", "history"]))
    assert len(entries) == 1 and not entries[0]["settings"].get("preview", False)
    entry = entries[0]
    assert entry["settings"]["input"] == ctx.fixture.TEXT
    assert entry["settings"]["instructions"] == ctx.fixture.DEFAULT_INSTRUCTIONS
    inspection = json.loads(public(["--format", "json", "audio", "inspect", entry["id"]]))
    assert inspection["analysis"]["duration"] == 2
    original = public(["audio", "export", entry["id"], "--output", "-"])
    assert original == ctx.fixture.WAV, "Successful retry did not retain original WAV bytes"
    (ctx.args.output / "failure-inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
    (ctx.args.output / "failure-recovered.wav").write_bytes(original)
    terminal.send(b"\x1b")
    main_form()
    terminal.send(b"\t\t" + ctx.picker.END + b"\r")
    visible_failure(("More actions", "[Esc] Back"), absent=("Last error",))
    terminal.send(b"\x1b")
    main_form()
    visible_failure(("› More actions…",))
    terminal.send(b"\t")
    visible_failure(("00:00.000 / 00:02.000", "› Play audio"))
    result["failure_checks"] = {
        "http_status": 401, "request_id": ctx.fixture.FAILURE_REQUEST_ID,
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
        disabled_instructions = {
            40: "Instructions Not used by this m…",
            90: "Instructions Not used by this model",
        }[width]
        failed_terminal = ctx.new_terminal(str(ctx.args.binary), ["audio", "speak"], env, width=width, height=height)
        ctx.wait(failed_terminal, "Text to speech")
        failed_terminal.send(b"\t")
        ctx.wait(failed_terminal, ("╭ Instructions ", "› Instructions"))
        visible_failure((ctx.fixture.DEFAULT_INSTRUCTIONS,), target=failed_terminal)
        failed_terminal.send(b"\x1b[Z")
        ctx.paste(failed_terminal, ctx.fixture.TEXT)
        failed_terminal.send(b"\t\t\r" + ctx.picker.DOWN * 3 + b"\r")
        visible_failure(("tts-1", disabled_instructions), target=failed_terminal)
        failed_terminal.send(ctx.picker.DOWN + b"\r")
        visible_failure(("› coral",), target=failed_terminal)
        failed_terminal.send(ctx.picker.UP * 2)
        visible_failure(("› alloy",), target=failed_terminal)
        failed_terminal.send(b"\r")
        visible_failure(("› Voice Alloy",), target=failed_terminal)
        failed_terminal.send(ctx.picker.UP + b"\r" + ctx.picker.UP * 3 + b"\r")
        failed_terminal.send(b"\x1b[Z")
        ctx.wait(failed_terminal, ("╭ Instructions ", "› Instructions"))
        visible_failure((ctx.fixture.DEFAULT_INSTRUCTIONS,), target=failed_terminal)
        failed_terminal.send(b"\t\r" + ctx.picker.DOWN * 3 + b"\r")
        visible_failure((disabled_instructions, "Alloy"), target=failed_terminal)
        assert len(server.records) == 2, "Legacy model selection submitted speech"
        ctx.mark(failed_terminal, "failure-legacy-ready", result)
        failed_terminal.send(b"\x07")
        visible_failure((summary, "API setup", "Back to form"), target=failed_terminal)
        ctx.mark(failed_terminal, "failure-error-exit-recovery", result)
        assert len(server.records) == 3 and server.records[2].get("response_status") == 401
        legacy = server.records[2]["body"]
        assert legacy["model"] == "tts-1" and legacy["voice"] == "alloy"
        assert legacy["input"] == ctx.fixture.TEXT and "instructions" not in legacy
        failed_terminal.send(b"\x1b")
        main_form(failed_terminal)
        visible_failure(("› More actions…",), target=failed_terminal)
        assert failed_terminal.child.poll() is None, "First Escape bypassed the recovery return"
        assert len(server.records) == 3, "Recovery Escape retried speech"
        ctx.mark(failed_terminal, "failure-error-exit-form", result)
        failed_terminal.send(b"\x1b")
        failed_terminal.finish(1)
        ctx.check_frame_boundaries(failed_terminal)
        safe_screen(failed_terminal)
        restored = bytes(failed_terminal.raw).rsplit(b"\x1b[?2004l", 1)
        assert len(restored) == 2, "Error exit omitted terminal restoration"
        diagnostic = ctx.picker.ANSI.sub("", restored[1].decode("utf-8", errors="strict"))
        assert protocol_summary in diagnostic, "Error exit omitted its HTTP status after terminal restoration"
        assert "Request ID: " + ctx.fixture.FAILURE_REQUEST_ID in diagnostic
        assert "Check API key, organization and project." in diagnostic
        assert "Key setup:" in diagnostic and "help setup" in diagnostic
        ctx.mark(failed_terminal, "failure-error-exit-summary", result)
        observer = failed_terminal.navigation_observer
        retained = observer.scrollback_lines()
        visible = observer.viewport_text()
        all_rows = visible + "\n" + "\n".join(line for lines in retained.values() for line in lines)
        assert "Text to speech" not in all_rows and summary not in all_rows, "Error exit retained owned form rows"
        assert not any(event[1] == "r" for event in failed_terminal.events)
        assert observer.provenance()["bytes_observed"] == len(failed_terminal.raw)
        failed_terminal.save(ctx.args.output / "failure-error-exit")
        (ctx.args.output / "failure-error-exit-final.txt").write_text(visible)
        (ctx.args.output / "failure-error-exit-scrollback.json").write_text(json.dumps(retained, ensure_ascii=False, indent=2) + "\n")
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
                failed_terminal.save(ctx.args.output / "failure-error-exit-latest")
            finally:
                failed_terminal.close()
    result["failure_session_count"] = 2
    result["failure_total_request_count"] = len(server.records)
    assert result["failure_total_request_count"] == 3


def navigation_case(ctx):
    terminal = ctx.terminal
    server = ctx.server
    result = ctx.result
    up, down, ss3_up, ss3_down = b"\x1b[A", b"\x1b[B", b"\x1bOA", b"\x1bOB"
    backtab = b"\x1b[Z"
    focus = {"text": ("╭─", "More actions…"), "instructions": "› Instructions", "model": "› Model",
             "more": "› More actions…", "generate": "› Generate speech", "history": "› History",
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
        ctx.drain(terminal, .15)
        ctx.wait(terminal, focus.get(target, target), absent=absent)
        zero_requests()
        result["navigation_checks"].append({"name": name, "focus": target})

    def open_more(name):
        check(name + " select More", "more", ctx.picker.END)
        check(name + " open More", "history", b"\r")

    ctx.mark(terminal, result["case"] + "-initial", result)
    check("empty text CSI Down", "instructions", down)
    check("initial Instructions example", ctx.fixture.DEFAULT_INSTRUCTIONS)
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
    check("End selects More", "more", ctx.picker.END)
    check("More Down stays bounded without result", "more", down)
    check("More Up selects History", "history", up)
    check("History Up selects Generate", "generate", up)
    check("Generate Down selects History", "history", ss3_down)
    check("History Down selects More", "more", down)
    check("Tab More to Text for long command", "text", b"\t")
    ctx.paste(terminal, "Command inspection sample. " * 180)
    check("long command draft stays local", "text")
    check("return to More for command inspection", "more", b"\t\t")
    check("open More command route", "history", b"\r")
    check("select Show command", "› Show command", down * 3)
    check("open Command information", "command", b"\r")
    check("Command starts with printf", "printf")
    initial_command = ctx.screen(terminal)
    check("Command End scrolls", "command", ctx.picker.END)
    assert ctx.screen(terminal) != initial_command, "Command modal did not scroll through its complete command"
    check("Command Down stays at bottom", "command", down * 4)
    check("Command Home returns to first rows", "printf", ctx.picker.HOME)
    check("Command Up stays in its modal", "command", up * 4)
    check("Command Escape returns to More", "› Show command", b"\x1b")
    check("More Home selects History", "history", ctx.picker.HOME)
    check("More Help is selectable", "› Help", down * 4)
    check("Help opens locally", "Use Tab to move between sections.", b"\r")
    check("Help End remains local", "Help", ctx.picker.END)
    check("Help Escape returns to More", "› Help", b"\x1b")
    check("More End selects Help", "› Help", ctx.picker.END)
    check("More Down stays at last action", "› Help", down)
    check("Escape returns to Settings More", "more", b"\x1b")
    check("open More to clear long command draft", "history", b"\r")
    check("select Clear for long command draft", "› Clear form", down * 2)
    check("clear long command draft locally", "more", b"\r")
    check("Home selects Model", "model", ctx.picker.HOME)
    check("Model Up enters Instructions", "instructions", up)
    check("Instructions Up enters Text", "text", up)

    editor_columns = terminal.initial_size[0] - 8
    result["exact_width_editor_columns"] = {}
    for editor in ("text", "instructions"):
        editor_columns = terminal.initial_size[0] - 8
        if editor == "instructions" and terminal.initial_size[0] >= 60 and terminal.initial_size[1] >= 24:
            editor_columns = min(100, terminal.initial_size[0] - 4) - 17
        result["exact_width_editor_columns"][editor] = editor_columns
        for name, value in (("single-line", "Boundary"), ("multiline", "First line\nSecond line\nThird line"),
                            ("wrapped-unicode", "界🙂e\u0301 " * 26), ("exact-width-unicode", "界" * editor_columns)):
            prefix = editor + " " + name
            if editor == "instructions":
                check(prefix + " enter editor", "instructions", down)
            ctx.paste(terminal, value)
            check(prefix + " paste keeps editor", editor)
            if name == "exact-width-unicode":
                # Two complete visual rows leave the editor's padding row.
                check(prefix + " End reaches padding row", editor, ctx.picker.END)
            if name == "single-line":
                for label, keys in (("Shift Down", b"\x1b[1;2B"), ("Alt Down", b"\x1b[1;3B"),
                                    ("Ctrl Down", b"\x1b[1;5B"), ("selection Down", b"\x1b[1;2D" + down)):
                    check(editor + " " + label + " remains editing", editor, keys)
                    check(editor + " " + label + " deselect", editor, b"\x1b[C")
                check(editor + " Alt Enter inserts a newline", editor, b"\x1b\r")
                ctx.paste(terminal, "Second")
            check(prefix + " Up stays within visual rows", editor, ss3_up)
            row = "padding row" if name == "exact-width-unicode" else "last visual row"
            check(prefix + " Down returns to " + row, editor, down)
            if name == "wrapped-unicode" and editor == "text":
                ctx.mark(terminal, result["case"] + "-wrapped-unicode", result)
            check(prefix + " last-row Down leaves editor", "instructions" if editor == "text" else "model", ss3_down)
            if editor == "text":
                check(prefix + " Tab returns to Settings", "model", b"\t")
            open_more(prefix + " clear")
            check(prefix + " select Clear", "› Clear form", down * 2)
            check(prefix + " Clear returns to More setting", "more", b"\r")
            visible = ctx.screen(terminal)
            assert value.splitlines()[0] not in visible and ctx.fixture.DEFAULT_INSTRUCTIONS not in visible
            check(prefix + " return to Model", "model", ctx.picker.HOME)
            check(prefix + " return to Instructions", "instructions", up)
            check(prefix + " return to Text", "text", up)

    check("select Settings for History", "model", b"\t\t")
    open_more("History")
    check("open local History", "Enter open · Esc back", b"\r")
    check("restore local result", "play", b"\r")
    ctx.wait(terminal, "00:00.000 / 00:02.000")
    ctx.mark(terminal, result["case"] + "-restored-result", result)
    check("reverse Tab Result to Settings", "more", backtab)
    check("More Down enters Play", "play", down)
    check("Result Down selects Save", "save", ss3_down)
    check("Result Down stays on Save", "save", down)
    check("Result Left returns to Play", "play", b"\x1b[D")
    check("Result Up focuses waveform", "←→ seek", ss3_up)
    check("Waveform Up enters Settings More", "more", ss3_up)
    check("Tab Settings to Result", "play", b"\t")
    check("Result End selects Save", "save", ctx.picker.END)
    check("Result Home selects Play", "play", ctx.picker.HOME)
    check("Tab Result wraps to Text", "text", b"\t")
    check("reverse Tab Text returns to Result", "play", backtab)
    check("Tab Result wraps again", "text", b"\t")
    check("Tab Text to Instructions with result", "instructions", b"\t")
    check("Tab Instructions to Settings with result", "more", b"\t")
    check("Tab Settings to Result again", "play", b"\t")
    check("reverse Tab Result to Settings again", "more", backtab)
    visible = ctx.screen(terminal)
    assert "Command preview" not in visible and "printf" not in visible, "Main form retained advanced command content"
    ctx.mark(terminal, result["case"] + "-complete", result)
    result["navigation_check_count"] = len(result["navigation_checks"])
    # Direct History adds a stop between Generate and More in both directions.
    assert result["navigation_check_count"] == 188, "Navigation inventory changed"
    zero_requests()
    terminal.send(b"\x03")
    terminal.finish(130)
    zero_requests()

    assert json.loads(ctx.public(["--format", "json", "audio", "history"])) == ctx.seeded_entry_list
    ctx.result["seeded_history_preserved"] = True


def startup_case(ctx):
    server = ctx.server
    result = ctx.result
    env = ctx.env
    private = ctx.private
    programs = ctx.programs
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
    on_path.symlink_to(ctx.args.binary.resolve())
    assert os.path.samefile(on_path, ctx.args.binary), "PATH selected a different executable"
    startup_env["AUDIO_EXPECTED_COMMAND"] = str(on_path)
    cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
    entry = ctx.seed_history(private, env)
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
            child = ctx.new_terminal(path, [*options, "-c", parent_script, "audio-startup", *arguments], command_env,
                             width=90, height=34)
            try:
                if form:
                    ctx.wait(child, "Text to speech")
                    child.send(b"\x03")
                child.wait("__AUDIO_STARTUP_STATUS__=", timeout=12)
                statuses = re.findall(r"__AUDIO_STARTUP_STATUS__=(\d+)", child.text())
                assert statuses, "Native parent did not report the CLI status"
                code = int(statuses[-1])
                assert code == (130 if form else 0), (name, label, code)
                assert child.child.poll() is None, "Native parent exited before its release handshake"
                ctx.check_live_parent_terminal(child, form)
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
                child.save(ctx.args.output / ("startup-" + name + "-" + label))
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
                        assert destination.read_bytes() == ctx.fixture.WAV
                assert {str(p): p.read_bytes() for p in canaries} == expected_profiles, "Startup changed a profile canary"
                assert files(config) == expected_config, "Startup wrote optional shell configuration"
                observed_cache = files(cache)
                if observed_cache != expected_cache:
                    result["startup_cache_change"] = {"shell": name, "command": command, "position": position,
                                                       "expected": expected_cache, "actual": observed_cache}
                assert observed_cache == expected_cache, "Startup changed existing private history"
        assert not server.records, "Local startup coverage reached the synthetic API"
    assert result["commands"], "No supported native shell was available"
    result.update(pass_=True, exit_status=0, requests=[])
    result["pass"] = result.pop("pass_")


def print_command_case(ctx):
    server = ctx.server
    result = ctx.result
    env = ctx.env
    private = ctx.private
    programs = ctx.programs
    deadline = time.monotonic() + 60
    on_path = programs / "openai"
    on_path.symlink_to(ctx.args.binary.resolve())
    assert os.path.samefile(on_path, ctx.args.binary), "PATH selected a different executable"
    command_env = dict(env, AUDIO_EXPECTED_COMMAND=str(on_path), SHELL="/bin/bash")
    parent_script = ('[ "$(command -v openai)" = "$AUDIO_EXPECTED_COMMAND" ] || exit 90\n'
                     'openai audio speak\naudio_status=$?\n'
                     'printf "\\n__AUDIO_PRINT_STATUS__=%s\\n" "$audio_status"\n'
                     'IFS= read -r audio_release\nexit "$audio_status"\n')
    cache = private / "Library/Caches" if sys.platform == "darwin" else Path(env["XDG_CACHE_HOME"])
    history = cache / "openai/audio"
    assert not history.exists(), "Print case requires fresh isolated History"
    text = "A" * 60
    body = {"input": text, "instructions": ctx.fixture.DEFAULT_INSTRUCTIONS,
            "model": "gpt-4o-mini-tts", "response_format": "wav", "speed": 1, "voice": "coral"}
    body_text = json.dumps(body, sort_keys=True, separators=(",", ":"))
    expected = ("printf %s '" + body_text + "' | OPENAI_UNTRUSTED_STDIN=true "
                "openai audio speak --output speech.wav").encode()
    result["commands"] = []
    result["case_deadline_seconds"] = 60
    result["coverage_limit"] = "Private PTY lifecycle and emitted bytes; no native clipboard or appearance claim."

    def remaining():
        budget = deadline - time.monotonic()
        assert budget > 0, "Print case exceeded its sixty-second execution deadline"
        return min(5, budget)

    def zero_requests():
        with server.lock:
            handlers = len(server.handler_threads)
        assert handlers == 0 and not server.records and not server.failures, "Printing reached the speech fixture"
        assert not history.exists(), "Printing created private audio History"

    for action, expected_status in (("print", 0), ("cancel", 130)):
        assert deadline - time.monotonic() >= 5, "Insufficient time to launch another print session"
        child = ctx.new_terminal("/bin/bash", ["--noprofile", "--norc", "-c", parent_script], command_env,
                         width=40, height=12)
        try:
            child.wait("Text to speech", timeout=remaining())

            def send_and_wait(keys, marker):
                offset = len(child.raw)
                child.send(keys)
                child.wait(marker, timeout=remaining(), after=offset)
                zero_requests()

            ctx.paste(child, text)
            send_and_wait(b"\t\t" + ctx.picker.END, "More actions")
            send_and_wait(b"\r", "History")
            send_and_wait(ctx.picker.DOWN * 3, "Show command")
            send_and_wait(b"\r", "Ctrl+P print/exit")
            child.send(b"\r")
            ctx.drain(child, min(.2, remaining()))
            zero_requests()
            assert child.child.poll() is None, "Enter unexpectedly exited Show command"
            offset = len(child.raw)
            child.send(b"\x10" if action == "print" else b"\x03")
            child.wait("__AUDIO_PRINT_STATUS__=", timeout=remaining(), after=offset)
            ctx.drain(child, min(.1, remaining()))
            statuses = re.findall(rb"__AUDIO_PRINT_STATUS__=(\d+)", child.raw[offset:])
            assert statuses == [str(expected_status).encode()], "Print/cancel changed the CLI exit status"
            assert child.child.poll() is None, "Native parent exited before the restoration check"
            ctx.check_live_parent_terminal(child, True)
            zero_requests()
            paste_off = child.raw.rfind(b"\x1b[?2004l")
            cursor_on = child.raw.rfind(b"\x1b[?25h")
            assert offset <= paste_off < cursor_on, "Final frame cleanup did not precede command output"
            status_start = child.raw.index(b"__AUDIO_PRINT_STATUS__=", cursor_on)
            emitted = bytes(child.raw[cursor_on + len(b"\x1b[?25h"):status_start])
            newline = (b"\r\n" if child.initial[1] & termios.OPOST and child.initial[1] & termios.ONLCR
                       else b"\n")
            # The parent's status marker contributes one leading newline.
            want = expected + newline * 2 if action == "print" else newline
            assert emitted == want, "Final command bytes changed or printing preceded cleanup"
            (ctx.args.output / ("print-command-" + action + "-emitted.bin")).write_bytes(emitted)
            child.send(b"release-parent\n")
            while child.child.poll() is None:
                child.read(min(.03, remaining()))
            child.child.wait(timeout=1)
            ctx.drain_after_exit(child, result)
            assert child.child.returncode == expected_status, "Native parent changed the CLI exit status"
            result["commands"].append({"action": action, "exit_status": expected_status,
                                       "native_modes_checked_before_parent_release": True,
                                       "frame_cleanup_before_command": True,
                                       "emitted_sha256": hashlib.sha256(emitted).hexdigest(),
                                       "printed_command_bytes": len(expected) if action == "print" else 0,
                                       "requests": 0, "history_created": False})
        finally:
            try:
                child.save(ctx.args.output / ("print-command-" + action))
            finally:
                child.close()
    zero_requests()
    result.update(exit_status=0, requests=[], initial_size=[40, 12], pass_=True)
    result["pass"] = result.pop("pass_")


def workflow_case(ctx):
    terminal = ctx.terminal
    server = ctx.server
    result = ctx.result
    public = ctx.public
    private = ctx.private
    playback = ctx.playback

    def player_record(index):
        deadline = time.monotonic() + 3
        while True:
            records = playback.read_text().splitlines() if playback.exists() else []
            if len(records) > index:
                return json.loads(records[index])
            assert time.monotonic() < deadline, "The fake player did not receive generated audio"
            ctx.drain(terminal, .05)

    def marker_column(visible):
        # Asciinema text preserves soft wraps as one logical line. This fixture
        # uses only single-cell characters, so recorded widths recover its rows.
        width = terminal.initial_size[0]
        for event in terminal.events:
            if event[1] == "r":
                width = int(event[2].split("x")[0])
        physical = []
        for line in visible.splitlines():
            assert all(32 <= ord(char) < 127 or char in "│┃╷╵█▀▄─·›…←→↑↓" for char in line), \
                "Waveform fixture contains a character with an unverified cell width"
            physical.extend([line[start:start + width] for start in range(0, len(line), width)] or [""])
        indices = [index for index, row in enumerate(physical) if "┃" in row]
        assert len(indices) == 5, "The result did not expose its five-row waveform"
        assert indices == list(range(indices[0], indices[0] + 5)), "Waveform rows were not contiguous"
        rows = [physical[index] for index in indices]
        assert all(row.count("┃") == 1 for row in rows), "A waveform row contained multiple playback markers"
        columns = {row.index("┃") for row in rows}
        assert len(columns) == 1, "The playback marker did not span the waveform"
        return columns.pop()

    def advance_player(played, initial, following, clock):
        assert played["stats"], "Workbench playback did not request FFplay status"
        ctx.drain(terminal, .2)
        before = ctx.wait(terminal, initial)
        before_column = marker_column(before)
        # The fake clock remains fixed until this process-specific release.
        # Wall-clock passage alone must not advance the displayed position.
        ctx.drain(terminal, .3)
        assert initial in ctx.screen(terminal), "Playback advanced without a helper clock report"
        ctx.playback_clock.write_text(json.dumps({"pid": played["pid"], "clock": clock}))
        ctx.wait(terminal, following)
        # A PTY read can end after the timeline and before the action rows.
        # Require both values in one snapshot before judging playback state.
        after = ctx.wait(terminal, "Stop audio")
        assert following in after, "Reported playback position disappeared before its controls appeared"
        after_column = marker_column(after)
        assert after_column > before_column, "Helper progress did not move the waveform marker"
        assert "Stop audio" in after, "Playback ended before its reported position appeared"
        return {"reported_seconds": clock, "before": initial, "after": following,
                "marker_before": before_column, "marker_after": after_column,
                "stationary_before_report": True}

    def saved_file(path):
        deadline = time.monotonic() + 3
        while not path.exists() or path.read_bytes() != ctx.fixture.WAV:
            assert time.monotonic() < deadline, "Save did not export the complete original recording"
            ctx.drain(terminal, .05)
        ctx.wait(terminal, "Saved:", absent=("Saving audio", "Enter save · Esc back"))

    result["resize_checkpoints"] = not ctx.args.no_resize
    ctx.mark(terminal, "initial-form", result)
    ctx.paste(terminal, ctx.fixture.TEXT)
    terminal.send(b"\t")
    visible = ctx.wait(terminal, "› Instructions")
    assert "╭" in visible and "╰" in visible, "Instructions replaced the primary input box"
    terminal.send(b"\x01\x0b")
    ctx.paste(terminal, ctx.fixture.INSTRUCTIONS)
    ctx.mark(terminal, "instructions-editor", result)
    terminal.send(b"\t\r")
    ctx.wait(terminal, "gpt-4o-mini-tts-2025-12-15")
    terminal.send(ctx.picker.DOWN * 2)
    ctx.mark(terminal, "model-choices", result)
    terminal.send(b"\r")
    visible = ctx.wait(terminal, "Model          gpt-4o-mini-tts-2025-12-15")
    assert "╭" in visible and "╰" in visible, "Settings replaced the primary input box"
    ctx.mark(terminal, "settings-form", result)
    terminal.send(ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Enter select · Esc back")
    terminal.send(ctx.picker.UP * 3)
    ctx.mark(terminal, "voice-choices", result)
    terminal.send(b"\r")
    ctx.wait(terminal, "Voice          Ash")
    terminal.send(ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Response format")
    ctx.mark(terminal, "format-choices", result)
    terminal.send(ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Format         PCM")
    terminal.send(b"\r" + ctx.picker.UP + b"\r")
    ctx.wait(terminal, "Format         WAV")
    terminal.send(ctx.picker.DOWN + b"\x1b[C")
    ctx.wait(terminal, "Speed          1.25 x")
    ctx.mark(terminal, "selected-settings", result)
    assert not server.records, "Choosing or typing generated speech"
    terminal.send(b"\x07")
    visible = ctx.wait(terminal, "00:00.000 / 00:02.000")
    assert "More actions…" not in visible and "Instructions" not in visible, "Playback still displayed the editing form"
    marker_column(visible)
    ctx.mark(terminal, "generated-result", result)
    assert len(server.records) == 1, "Generation request count changed"
    request = server.records[0]["body"]
    assert request["input"] == ctx.fixture.TEXT and request["instructions"] == ctx.fixture.INSTRUCTIONS
    assert (request["model"], request["voice"], request["response_format"], request["speed"]) == (
        "gpt-4o-mini-tts-2025-12-15", "ash", "wav", 1.25)
    entries = json.loads(public(["--format", "json", "audio", "history"]))
    assert len(entries) == 1 and not entries[0]["settings"].get("preview", False)
    entry = entries[0]
    inspection = json.loads(public(["--format", "json", "audio", "inspect", entry["id"]]))
    assert inspection["analysis"]["duration"] == 2
    peaks = inspection["analysis"]["peaks"]
    assert len(peaks) > 8 and max(peaks)-min(peaks) > .5, "Waveform did not use varying samples"
    (ctx.args.output / "inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
    terminal.send(b" ")
    ctx.wait(terminal, "Stop audio")
    played = player_record(0)
    assert played["sha256"] == hashlib.sha256(ctx.fixture.WAV).hexdigest()
    progress = [advance_player(played, "00:00.000 / 00:02.000", "00:00.600 / 00:02.000", .6)]
    ctx.mark(terminal, "playback-progress", result)
    terminal.send(b" ")
    ctx.wait(terminal, "Play audio")
    terminal.send(b"\x1b[A")
    ctx.wait(terminal, "←→ seek")
    terminal.send(b"\x1b[D" * 35)
    ctx.wait(terminal, "00:00.000 / 00:02.000")
    # Seven boundaries at the wide layout's 35 bars select exactly 400 ms.
    terminal.send(b"\x1b[C" * 7)
    ctx.wait(terminal, "00:00.400 / 00:02.000")
    ctx.mark(terminal, "selected-playback-start", result)
    assert len(playback.read_text().splitlines()) == 1, "Seeking started playback"
    assert len(server.records) == 1, "Seeking generated speech"
    terminal.send(b" ")
    ctx.wait(terminal, "Stop audio")
    played = player_record(1)
    samples = ctx.fixture.WAV[44 + 9600 * 2:]
    header = bytearray(ctx.fixture.WAV[:44])
    struct.pack_into("<I", header, 4, 36 + len(samples))
    struct.pack_into("<I", header, 40, len(samples))
    expected = bytes(header) + samples
    assert played["bytes"] == len(expected)
    assert played["sha256"] == hashlib.sha256(expected).hexdigest(), "Player received the wrong selected samples"
    progress.append(advance_player(played, "00:00.400 / 00:02.000", "00:01.000 / 00:02.000", .6))
    # Seeking an active result stops it without starting another helper.
    terminal.send(b"\x1b[D" * 35)
    ctx.wait(terminal, "00:00.000 / 00:02.000")
    ctx.wait(terminal, "Play audio")
    assert len(playback.read_text().splitlines()) == 2, "Seeking restarted playback"
    result["seek"] = {"offset_ms": 400, "frames_skipped": 9600,
                      "played_bytes": len(expected), "explicit_play": True,
                      "seek_stopped_playback": True, "requests": 1}
    result["playback_progress"] = progress
    terminal.send(b"s")
    ctx.wait(terminal, "Filename")
    terminal.send(b"\t\x01\x0b-\r")
    ctx.wait(terminal, "Enter a filename for saving.")
    assert len(server.records) == 1, "Invalid save path generated speech"
    ctx.mark(terminal, "save-validation", result)
    destination = private / "saved speech.wav"
    terminal.send(b"\x01\x0b")
    ctx.paste(terminal, str(destination))
    terminal.send(b"\r")
    saved_file(destination)
    assert destination.read_bytes() == ctx.fixture.WAV
    (ctx.args.output / "exported.wav").write_bytes(destination.read_bytes())
    ctx.mark(terminal, "saved-result", result)
    default_folder = private / "Downloads" / "gpt-audio"
    assert not default_folder.exists(), "Custom Save created the default audio folder"
    default_name = "hello-from-the-audio-workbench-this-clip-is"
    defaults = [default_folder / (default_name + suffix + ".wav") for suffix in ("", "-2")]
    for path in defaults:
        terminal.send(b"s")
        ctx.wait(terminal, "Filename")
        ctx.mark(terminal, "save-default" + ("-2" if path == defaults[1] else ""), result)
        if path == defaults[1]:
            terminal.send(b"\t\t")
            ctx.wait(terminal, "› Save to")
            ctx.mark(terminal, "save-folder" + ("-2" if path == defaults[1] else ""), result)
            terminal.send(b"\t")
            ctx.wait(terminal, "› Save audio")
            if path == defaults[1]:
                terminal.send(b"\x1b[Z\x1b[Z")
                ctx.wait(terminal, "› Filename")
        terminal.send(b"\r")
        saved_file(path)
    assert all(path.read_bytes() == ctx.fixture.WAV for path in defaults)
    assert destination.read_bytes() == ctx.fixture.WAV, "Default Save changed the explicit export"
    result["default_save"] = {"directory": "~/Downloads/gpt-audio", "filenames": [path.name for path in defaults],
                              "prompt_derived": True, "numbered_collision": True, "original_bytes_preserved": True}
    ctx.mark(terminal, "default-saved-result", result)
    terminal.send(b"s")
    ctx.wait(terminal, "Filename")
    terminal.send(b"\t\x01\x0b")
    ctx.paste(terminal, "chosen-folder.wav")
    terminal.send(b"\t\x01\x0b")
    ctx.paste(terminal, str(private))
    terminal.send(b"\r")
    ctx.wait(terminal, "› Save audio")
    assert not (private / "chosen-folder.wav").exists(), "Committing the folder saved audio"
    terminal.send(b"\r")
    saved_file(private / "chosen-folder.wav")
    assert (private / "chosen-folder.wav").read_bytes() == ctx.fixture.WAV
    terminal.send(b"\x1b")
    ctx.wait(terminal, ("╭─", "More actions…"))
    played_before_listen = len(playback.read_text().splitlines())
    terminal.send(b"\x0f")
    ctx.wait(terminal, "Play audio")
    assert len(playback.read_text().splitlines()) == played_before_listen, "Listen shortcut started playback"
    ctx.mark(terminal, "listen-shortcut", result)
    terminal.send(b"\x1b")
    ctx.wait(terminal, ("╭─", "More actions…"))
    terminal.send(b"\t\t" + ctx.picker.END + ctx.picker.UP)
    ctx.wait(terminal, "› History")
    terminal.send(b"\r")
    ctx.wait(terminal, ctx.fixture.TEXT.splitlines()[0])
    ctx.mark(terminal, "history", result)
    terminal.send(b"\r")
    ctx.wait(terminal, "00:00.000 / 00:02.000")
    ctx.wait(terminal, "Play audio", absent=("Opening audio",))
    ctx.mark(terminal, "restored-result", result)
    if not ctx.args.no_resize:
        terminal.resize(40, 12)
        ctx.wait(terminal, "00:02.000")
        ctx.mark(terminal, "compact-40x12", result)
        terminal.resize(28, 8)
        ctx.wait(terminal, "Resize to 40 x 12.")
        terminal.send(b"\x07discarded")
        ctx.drain(terminal)
        assert len(server.records) == 1, "Small terminal accepted a hidden submission"
        ctx.mark(terminal, "too-small", result)
        terminal.resize(90, 34)
        ctx.wait(terminal, "00:00.000 / 00:02.000")
    terminal.send(b"\x1b")
    ctx.wait(terminal, ("╭─", "More actions…"))
    terminal.send(b"\t\t" + ctx.picker.HOME + ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Enter select · Esc back")
    terminal.send(ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Voice          Ballad")
    assert len(server.records) == 1, "Selecting an audition voice generated speech"
    terminal.send(ctx.picker.END + b"\r")
    ctx.wait(terminal, "› History")
    terminal.send(ctx.picker.DOWN + b"\r")
    ctx.wait(terminal, "Ballad · WAV")
    ctx.mark(terminal, "voice-audition", result)
    assert len(server.records) == 2 and server.records[1]["body"]["input"] == ctx.fixture.AUDITION
    assert json.loads(public(["--format", "json", "audio", "history"])) == entries
    terminal.send(b"\x1b")
    ctx.wait(terminal, ("╭─", "More actions…"))
    terminal.send(b"\t\t\t")
    ctx.wait(terminal, "Ash · WAV")
    terminal.send(b"\x1b")
    ctx.wait(terminal, ("╭─", "More actions…"))
    terminal.send(b"\t\t" + ctx.picker.END + b"\r")
    ctx.wait(terminal, "› History")
    terminal.send(ctx.picker.DOWN * 2)
    ctx.wait(terminal, "› Clear form")
    terminal.send(b"\r")
    ctx.wait(terminal, "› More actions…", absent=("Audio ready", ctx.fixture.TEXT.splitlines()[0], ctx.fixture.INSTRUCTIONS,
                                                ctx.fixture.DEFAULT_INSTRUCTIONS))
    ctx.mark(terminal, "cleared-form", result)
    assert json.loads(public(["--format", "json", "audio", "history"])) == entries
    assert destination.read_bytes() == ctx.fixture.WAV and len(server.records) == 2
    assert all(path.read_bytes() == ctx.fixture.WAV for path in defaults), "Clear removed a default export"
    terminal.send(b"\x03")
    terminal.finish(130)


def compact_case(ctx):
    terminal = ctx.terminal
    server = ctx.server
    result = ctx.result
    public = ctx.public
    ctx.mark(terminal, "compact-initial-form", result)
    ctx.paste(terminal, ctx.fixture.TEXT)
    assert not server.records, "Typing generated speech"
    terminal.send(b"\x07")
    ctx.wait(terminal, "00:00.000 / 00:02.000")
    assert len(server.records) == 1, "Compact generation request count changed"
    request = server.records[0]["body"]
    assert request["input"] == ctx.fixture.TEXT and request.get("instructions", "") == ctx.fixture.DEFAULT_INSTRUCTIONS
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
    assert original == ctx.fixture.WAV, "Compact export changed original audio bytes"
    (ctx.args.output / "compact-exported.wav").write_bytes(original)
    (ctx.args.output / "compact-inspection.json").write_text(json.dumps(inspection, indent=2) + "\n")
    ctx.mark(terminal, "compact-40x12", result)
    terminal.send(b"s")
    ctx.wait(terminal, "Filename")
    ctx.wait(terminal, "Enter save · Tab edit · Esc back")
    ctx.mark(terminal, "compact-save", result)
    terminal.send(b"\t\t")
    ctx.wait(terminal, "› Save to")
    ctx.wait(terminal, "Enter done · Esc back")
    ctx.mark(terminal, "compact-save-folder", result)
    terminal.send(b"\x1b")
    ctx.wait(terminal, "Play audio")
    assert not any(event[1] == "r" for event in terminal.events), "Fixed-size capture contains a resize"
    result["fixed_geometry"] = True
    result["original_audio_sha256"] = hashlib.sha256(original).hexdigest()
    terminal.send(b"\x03")
    terminal.finish(130)
    assert json.loads(public(["--format", "json", "audio", "history"])) == entries
    assert len(server.records) == 1, "Compact cancellation generated speech"


def wide_case(ctx):
    terminal, result, server = ctx.terminal, ctx.result, ctx.server
    visible = ctx.wait(terminal, "Text to speech")
    border = visible.split("╭", 1)[1].split("╮", 1)[0]
    assert len(border) + 2 == 96, "Wide composer escaped its reading column"
    ctx.mark(terminal, "wide-empty-form", result)
    draft = "Hello. This draft stays near its settings."
    ctx.paste(terminal, draft)
    terminal.send(b"\t\t" + ctx.picker.END)
    ctx.wait(terminal, "› More actions…")
    for keys, label in [(b"\x1b[D", "History"), (b"\x1b[D", "Generate speech"),
                        (b"\x1b[C", "History"), (b"\x1b[C", "More actions…")]:
        terminal.send(keys)
        ctx.wait(terminal, "› " + label)
        assert not server.records, "Horizontal navigation generated speech"
    ctx.mark(terminal, "wide-actions", result)
    terminal.send(b"\r")
    visible = ctx.wait(terminal, "› History")
    assert draft in visible and "Settings" in visible, "More actions lost form context"
    ctx.mark(terminal, "wide-more", result)
    terminal.send(b"\x1b")
    ctx.wait(terminal, "› More actions…")
    terminal.send(b"\x03")
    terminal.finish(130)
    assert not server.records and not server.failures, "Wide form navigation sent a request"
    result.update(reading_column=96, horizontal_navigation=True, zero_requests=True)


def busy_cancel_case(ctx):
    terminal = ctx.terminal
    server = ctx.server
    result = ctx.result
    public = ctx.public
    env = ctx.env
    private = ctx.private
    case = ctx.name
    ctx.paste(terminal, ctx.fixture.TEXT)
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


def ctrl_c_case(ctx):
    terminal = ctx.terminal
    terminal.send(b"\x03")
    terminal.finish(130)


def sigterm_case(ctx):
    terminal = ctx.terminal
    os.kill(terminal.child.pid, signal.SIGTERM)
    terminal.finish(143)


def stopped_reader_case(ctx):
    terminal = ctx.terminal
    result = ctx.result
    children = [process for process in ctx.process_snapshot() if process["ppid"] == terminal.child.pid]
    worker_names = {ctx.args.binary.name, ctx.args.binary.resolve().name}
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
    ctx.check_native_modes(terminal)
    result["native_modes_restored"] = True
    ctx.check_children_exited(children, result)
    ctx.drain_after_exit(terminal, result)
    ctx.check_frame_boundaries(terminal)
    paste_on, paste_off = terminal.raw.rfind(b"\x1b[?2004h"), terminal.raw.rfind(b"\x1b[?2004l")
    cursor_off, cursor_on = terminal.raw.rfind(b"\x1b[?25l"), terminal.raw.rfind(b"\x1b[?25h")
    result["ansi_cleanup"] = {
        "paste_on_offset": paste_on, "paste_off_offset": paste_off,
        "cursor_off_offset": cursor_off, "cursor_on_offset": cursor_on,
        "bracketed_paste_restored": paste_off > paste_on >= 0,
        "cursor_visibility_restored": cursor_on > cursor_off >= 0,
        "scope": "Observed only. Blocked output can prevent bounded visual cleanup.",
    }



SCENARIOS = {
    "workflow": workflow_case,
    "compact": compact_case,
    "wide": wide_case,
    "navigation": navigation_case,
    "navigation-compact": navigation_case,
    "failure": failure_case,
    "failure-compact": failure_case,
    "ctrl-c": ctrl_c_case,
    "sigterm": sigterm_case,
    "busy-ctrl-c": busy_cancel_case,
    "busy-sigterm": busy_cancel_case,
    "stopped-reader": stopped_reader_case,
    "startup": startup_case,
    "print-command": print_command_case,
}
