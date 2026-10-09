#!/usr/bin/env python3
"""Drive the real editor inside the shared recorder's terminal session."""

import errno
import fcntl
import importlib.util
import json
import os
import pty
import re
import select
import signal
import struct
import sys
import termios
import time


FIXTURE = "Hello, tokens! 👋\nCafé."
MODEL_NAMES = {"o200k_base": "GPT-5.x & o1/o3", "cl100k_base": "GPT-4 & GPT-3.5",
               "r50k_base": "GPT-3", "p50k_base": "Codex"}
CSI = re.compile(rb"\x1b\[[0-?]*[ -/]*[@-~]")
OSC = re.compile(rb"\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)")
QUERY = re.compile(rb"\x1b\[\?7\$p|\x1b\]11;\?(?:\x07|\x1b\\)")


def stop_editor(pid, terminal):
    # Failed recordings need no remaining frames. Release terminal backpressure
    # before waiting for the owned session leader to finish on Darwin.
    os.close(terminal)
    for number in (signal.SIGTERM, signal.SIGKILL):
        try:
            os.killpg(pid, number)
        except ProcessLookupError:
            # The owned process group can exit before this cleanup signal.
            pass
        deadline = time.monotonic() + 1
        while time.monotonic() < deadline:
            waited, value = os.waitpid(pid, os.WNOHANG)
            if waited:
                return os.waitstatus_to_exitcode(value)
            time.sleep(0.01)
    raise RuntimeError("editor did not exit within the recording cleanup deadline")


def drive():
    width = int(os.environ["DEMO_COLUMNS"])
    height = int(os.environ["DEMO_ROWS"])
    theme = os.environ["DEMO_THEME"]
    report_path = os.environ["DEMO_EDITOR_REPORT"]
    details = os.environ.get("DEMO_MODE") == "details"
    long_text = os.environ.get("DEMO_MODE") == "long-text"
    if long_text and ((width, height) not in ((80, 20), (40, 12)) or os.environ.get("DEMO_SCENE") not in ("before", "after")):
        raise ValueError("long-text recording requires before/after at 80x20 or 40x12")
    details_presentation = os.environ.get("DEMO_DETAILS_PRESENTATION", "historical")
    if details_presentation not in ("historical", "continuous") or details_presentation == "continuous" and not details:
        raise ValueError("continuous presentation requires details mode")
    if details and (width not in (40, 80) or height != 12 or os.environ.get("DEMO_SCENE") not in ("before", "after")):
        raise ValueError("details recording requires before/after at 40 or 80 columns and 12 rows")
    layout = os.environ.get("DEMO_EDITOR_LAYOUT", "options")
    if layout not in ("legacy", "options"):
        raise ValueError("DEMO_EDITOR_LAYOUT must be legacy or options")
    linked_setting = os.environ.get("DEMO_EDITOR_LINKED", "0")
    if linked_setting not in ("0", "1"):
        raise ValueError("DEMO_EDITOR_LINKED must be 0 or 1")
    linked = linked_setting == "1" or details or long_text
    if linked and (layout != "options" or not (details or long_text) and os.environ.get("DEMO_SCENE") != "after"):
        raise ValueError("linked recording requires the after Options editor")
    presentation = os.environ.get("DEMO_EDITOR_PRESENTATION", "encodings")
    if presentation not in ("encodings", "models"):
        raise ValueError("DEMO_EDITOR_PRESENTATION must be encodings or models")
    models = presentation == "models" or details or long_text
    if models and not linked:
        raise ValueError("model presentation requires the linked after Options editor")

    def encoding_row(encoding):
        if models:
            return "Model  " + MODEL_NAMES[encoding] + "  " + ("Default" if encoding == "o200k_base" else "Legacy")
        return ("Tokenizer" if layout == "options" else "Encoding") + "  " + encoding

    chooser_title = "Choose model" if models else "Choose tokenizer"
    gate_read, gate_write = os.pipe()
    pid, terminal = pty.fork()
    if pid == 0:
        os.close(gate_write)
        if os.read(gate_read, 1) != b"1":
            os._exit(96)
        os.close(gate_read)
        os.execvp("openai", ["openai", "tokenizer"])
    os.close(gate_read)
    try:
        fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", height, width, 0, 0))
        os.write(gate_write, b"1")
    except BaseException:
        os.close(gate_write)
        stop_editor(pid, terminal)
        raise
    else:
        os.close(gate_write)
    buffer = bytearray()
    queries = bytearray()
    events = []
    status = None
    eof = False

    def interrupt(number, _frame):
        raise InterruptedError(number)

    for number in (signal.SIGINT, signal.SIGTERM, signal.SIGHUP):
        signal.signal(number, interrupt)

    def write(data):
        view = memoryview(data)
        while view:
            count = os.write(terminal, view)
            if count <= 0:
                raise RuntimeError("terminal input stopped")
            view = view[count:]

    def pump(timeout):
        nonlocal status, eof, queries
        if not eof and select.select([terminal], [], [], timeout)[0]:
            try:
                data = os.read(terminal, 32768)
            except OSError as error:
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                # Forward actual CLI bytes. No fabricated frames enter the cast.
                sys.stdout.buffer.write(data)
                sys.stdout.buffer.flush()
                buffer.extend(data)
                del buffer[:-65536]
                queries.extend(data)
                while match := QUERY.search(queries):
                    if match.group().startswith(b"\x1b["):
                        write(b"\x1b[?7;1$y")
                    else:
                        color = b"ffff/ffff/ffff" if theme == "light" else b"1212/1414/1616"
                        write(b"\x1b]11;rgb:" + color + b"\x1b\\")
                    del queries[:match.end()]
                del queries[:-128]
            else:
                eof = True
        if status is None:
            waited, value = os.waitpid(pid, os.WNOHANG)
            if waited:
                status = os.waitstatus_to_exitcode(value)

    def wait_for(needle, predicate=None):
        deadline = time.monotonic() + 12
        while True:
            visible = bytes(buffer)
            if linked:
                # Each inline frame starts with CR + EraseScreenBelow.
                # Do not combine a new tokenizer label with an old equal count.
                visible = visible.rsplit(b"\r\x1b[J", 1)[-1]
            text = CSI.sub(b"", OSC.sub(b"", visible)).decode("utf-8", "replace")
            matched = predicate(text, visible.decode("utf-8", "replace")) if predicate is not None else needle in text
            if matched and (not linked or "Ctrl+C exit" in text):
                return text
            if status is not None or time.monotonic() >= deadline:
                raise RuntimeError("editor did not reach the requested state")
            pump(0.05)

    def pause(seconds=1.4):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            pump(max(0, min(0.05, deadline - time.monotonic())))

    def send(data, clear=True):
        if clear:
            buffer.clear()
        write(data)

    def mark(name):
        events.append({"state": name, "observed_at": time.time()})
        pause()

    def finish(result):
        send(b"\x03")
        deadline = time.monotonic() + 5
        while status is None or not eof:
            if time.monotonic() >= deadline:
                raise RuntimeError("editor did not exit after Ctrl+C")
            pump(0.05)
        if status != 130:
            raise RuntimeError(f"editor returned {status}, expected 130")
        result.update(theme=theme, columns=width, rows=height, layout=layout, states=events, exit_status=status)
        with open(report_path, "w", encoding="utf-8") as report:
            json.dump(result, report, indent=2)
            report.write("\n")
        return status

    def record_details():
        # Share exact row predicates with snapshot validation, including wrapping.
        path = os.path.join(os.path.dirname(__file__), "validate.py")
        spec = importlib.util.spec_from_file_location("tokenizer_demo_validation", path)
        checks = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checks)
        continuous = details_presentation == "continuous"
        style = "legacy" if os.environ["DEMO_SCENE"] == "before" and not continuous else "refined"
        tokens = ("wrapped" if os.environ["DEMO_SCENE"] == "before" else "continuous") if continuous else None
        inputs = checks.CONTINUOUS_INPUTS if continuous else checks.DETAILS_INPUTS
        current_input = ""

        def replace_input(kind, modal=True):
            nonlocal current_input
            if kind != "source":
                if modal:
                    send(b"\x1b")
                    wait_for("Enter details")
                send(b"\x1b")
                wait_for("Ctrl+C exit · ↓ options · Tab switch")
                # A standalone Escape can combine with a following CSI key.
                # Observe Text focus before End, then observe EOF before clear.
                send(b"\x1b[F", clear=False)
                if continuous and current_input == inputs["source"]:
                    wait_for("", lambda text, raw: checks.source_at_cursor(raw, current_input, len(current_input), width))
                else:
                    wait_for("", lambda text, raw: "\x1b[7m \x1b[27m" in raw if style == "refined" else
                             any(line.strip().startswith("› Text ") and line.strip().endswith("▏") for line in text.splitlines()))
            send(b"\x15\x1b[200~" + inputs[kind].encode() + b"\x1b[201~")
            pause(.2)
            count = ("4 tokens" if continuous else "6 tokens") if kind == "source" else "2 tokens" if kind == "partial" else "1 token"
            wait_for(count, lambda text, raw: count in [line.strip() for line in text.splitlines()] and "Updating" not in text)
            current_input = inputs[kind]

        def wait_state(state):
            if continuous:
                return wait_for("", lambda text, raw: checks.continuous_state(text, state, tokens, width, raw, theme))
            return wait_for("", lambda text, raw: checks.details_state(text, state, style, width, raw, theme))

        replace_input("source", modal=False)
        wait_state("source")
        mark("source")
        send(b"\x1b[H" + b"\x1b[C" * 13 if continuous else b"\x1b[D")
        wait_state("cursor")
        mark("cursor")
        if continuous:
            send(b"\x1b[F\x1b[D")
            wait_state("trailing-space")
            mark("trailing-space")
        send(b"\t\t\x1b[H\x1b[C\x1b[C")
        if continuous:
            wait_for("", lambda text, raw: checks.continuous_state(text, "results", tokens, width, raw, theme))
        else:
            wait_for("Token 3 of 6", lambda text, raw: "Token 3 of 6" in [line.strip() for line in text.splitlines()] and '›["b"]' in text)
        send(b"\x1b[A")
        wait_state("up-navigation")
        mark("up-navigation")

        for kind in ("ordinary", "partial", "overflow"):
            replace_input(kind, modal=kind != "ordinary")
            send(b"\t\t\x1b[H\r")
            state = kind if kind != "overflow" else "overflow-start"
            plain = wait_state(state)
            mark(state)
            if kind == "overflow":
                current = checks.details_frame(plain, kind, style, width)
                covered = set(range(current["start"], current["end"] + 1))
                for _ in range(current["total"]):
                    if current["end"] == current["total"]:
                        break
                    previous = current["start"]
                    send(b"\x1b[6~")
                    plain = wait_for("", lambda text, raw: (value := checks.details_frame(text, kind, style, width)) and value["start"] > previous)
                    current = checks.details_frame(plain, kind, style, width)
                    covered.update(range(current["start"], current["end"] + 1))
                    pause(.2)
                if covered != set(range(1, current["total"] + 1)):
                    raise RuntimeError("details scrolling omitted exact data rows")
                mark("overflow-end")
                send(b"\x1b[H")
                wait_state("overflow-home")
                mark("overflow-home")
        replace_input("ordinary")
        wait_state("recovery")
        mark("recovery")
        result = {"capture_version": 6 if continuous else 5, "details_style": style, "inputs": inputs}
        if continuous:
            result["token_presentation"] = tokens
        return finish(result)

    def record_long_text():
        path = os.path.join(os.path.dirname(__file__), "validate.py")
        spec = importlib.util.spec_from_file_location("tokenizer_demo_validation", path)
        checks = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checks)
        style = "legacy" if os.environ["DEMO_SCENE"] == "before" else "indexed"

        def observe(state):
            return wait_for("", lambda text, raw: checks.long_text_state(raw, state, style, width, theme))

        def step(state, keys):
            send(keys)
            observe(state)
            mark(state)

        def paste(value):
            send(b"\x15\x1b[200~" + value.encode() + b"\x1b[201~")

        paste(checks.LONG_INPUTS["multiline"])
        observe("multiline-end")
        mark("multiline-end")
        if style == "legacy":
            step("line-home", b"\x1b[H")
            step("line-end", b"\x1b[F")
        else:
            step("document-home", b"\x1b[1;5H")
            step("page-down", b"\x1b[6~")
            step("page-up", b"\x1b[5~")
            step("document-end", b"\x1b[1;5F")
        step("edited", b"!")
        step("restored", b"\x7f")
        paste(checks.LONG_INPUTS["long-line"])
        observe("long-line-end")
        mark("long-line-end")
        if style == "indexed":
            step("long-line-word-left", b"\x1b[1;5D")
            step("long-line-word-right", b"\x1b[1;5C")
        step("long-line-home", b"\x1b[H")
        send(b"\x1b[F")
        observe("long-line-end")  # Clear only after observing the source at EOF.
        paste(checks.LONG_INPUTS["recovery"])
        observe("recovery")
        mark("recovery")
        return finish({"capture_version": 7, "navigation": style, "inputs": checks.LONG_INPUTS})

    try:
        wait_for("Ctrl+C exit")
        pause(0.4)
        if details:
            return record_details()
        if long_text:
            return record_long_text()
        for character in "Hello, ":
            send(character.encode())
            pause(0.055)
        send(b"\x1b[200~" + "tokens! 👋".encode() + b"\x1b[201~\r\x1b[200~" +
             "Café.".encode() + b"\x1b[201~")
        wait_for("10 tokens" if layout == "options" else "10 tokens · 26 bytes")
        mark("text")
        if linked:
            send(b"\x1b[H\x1b[C")
            wait_for("C▏afé.")
            wait_for("Token 9 of 10")
            wait_for('·["afé"]')
            mark("caret")
        send(b"\t\x1b[C")
        wait_for("[Token IDs]")
        mark("ids")
        if layout == "options":
            send(b"\r")
            wait_for("Choose view")
            mark("view-choice")
            send(b"\x1b[B\r")
        else:
            send(b"\x1b[C")
        wait_for("[Bytes]")
        wait_for("10 tokens · 26 bytes")
        mark("bytes")
        # The default encoding splits this emoji across token byte boundaries.
        if layout == "options":
            send(b"\t")
            wait_for("Enter details")
        # Arrow keys can leave the unchanged Results footer out of a redraw.
        # Caret-linked editors can arrive at the last token after typing.
        token_right = b"\x1b[C" if layout == "options" else b"\x1b[B"
        send(b"\x1b[H" + token_right * 4, clear=False)
        wait_for("Token 5 of 10" if layout == "options" else "Token 5/10 · ID 61138")
        wait_for("›[20 f0 9f 91]")
        wait_for("Enter details")
        mark("results")
        send(b"\r")
        wait_for("Token details · exact bytes")
        wait_for("partial UTF-8")
        wait_for("ID 61138")
        wait_for("20 f0 9f 91")
        mark("details")
        send(b"\x1b")
        wait_for("Enter details")
        if layout == "options":
            send(b"\x1b")
            wait_for("↓ options" if models else "Tab options")
            send(b"\t\x1b[B\r")
            wait_for(chooser_title)
            mark("tokenizer-choice")
            send(b"\x1b[B")
        else:
            send(b"\t\x1b[B")
        wait_for(MODEL_NAMES["cl100k_base"] if models else "cl100k_base")
        pause(0.2)
        send(b"\r")
        wait_for("11 tokens · 26 bytes")
        send(b"\x1b[Z")
        wait_for(encoding_row("cl100k_base"))
        mark("encoding")
        final_encoding = "cl100k_base"
        if linked:
            for index, encoding in [(2, "r50k_base"), (3, "p50k_base")]:
                # Text -> Options; choose its Tokenizer row independently of
                # the row remembered from the preceding selection.
                send(b"\t\x1b[H\x1b[B\r")
                wait_for(chooser_title)
                send(b"\x1b[H" + b"\x1b[B" * index + b"\r")
                wait_for(encoding_row(encoding))
                wait_for("11 tokens · 26 bytes")
                send(b"\x1b[Z")
                wait_for(encoding_row(encoding))
                wait_for("11 tokens · 26 bytes")
                mark(encoding.removesuffix("_base"))
                final_encoding = encoding
        send(b"\x1bOP")
        wait_for("Tokenizer · controls")
        mark("controls")
        send(b"\x1b")
        wait_for(encoding_row(final_encoding))
        pause(0.3)
        result = {"input": FIXTURE, "input_bytes": len(FIXTURE.encode()), "theme": theme,
                  "columns": width, "rows": height, "layout": layout,
                  "capture_version": 4 if models else 3 if linked else 2,
                  "input_actions": {"typed": "Hello, ", "pasted": ["tokens! 👋", "Café."], "newline": "Enter"},
                  "states": events, "exit_status": status}
        if linked:
            result["caret_actions"] = {"keys": ["Home", "Right"], "expected_byte_offset": 21}
            result["tokenizer_actions"] = ["cl100k_base", "r50k_base", "p50k_base"]
        if models:
            result["presentation"] = presentation
        return finish(result)
    finally:
        if status is None:
            status = stop_editor(pid, terminal)
        else:
            os.close(terminal)


if __name__ == "__main__":
    try:
        sys.exit(drive())
    except InterruptedError as error:
        sys.exit(128 + int(error.args[0]))
    except Exception as error:
        print(f"Editor recording failed: {error}", file=sys.stderr)
        sys.exit(97)
