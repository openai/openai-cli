#!/usr/bin/env python3
"""Check native Bash Readline against a built CLI without sending API requests."""
import importlib.util
import os
import pathlib
import signal
import sys
import tempfile
import time


def main():
    deadline = time.monotonic() + 60
    binary, bash = (str(pathlib.Path(arg).resolve()) for arg in sys.argv[1:])
    spec = importlib.util.spec_from_file_location(
        "terminal_harness", pathlib.Path(__file__).with_name("image_picker_harness.py"))
    harness = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(harness)
    # WSL can retain the session leader until EOF. Keep the existing Darwin ownership path.
    retain_identity = sys.platform.startswith("linux") and all(
        hasattr(os, name) for name in ("waitid", "WNOWAIT", "P_PID"))
    assigned = [b"transcribe", b"--file=assets/fixture name.json"]
    later_flag = " --format=json"
    cases = [
        ("single quoted assigned file", "--file='assets/fi'", assigned),
        ("double quoted assigned file", '--file="assets/fi"', assigned),
        ("double quoted apostrophe", '--file="assets/quote\'s"',
         [b"transcribe", b"--file=assets/quote's recording.wav"]),
        ("escaped assigned file", r"--file=assets/fixture\ n", assigned),
        ("separate quoted file", '--file "assets/fi"', [b"transcribe", b"--file", b"assets/fixture name.json"]),
        ("unquoted assigned control", "--file=assets/fi", assigned),
        ("UTF-8 midline file with later flag", "--file=assets/雪-fi" + later_flag,
         [b"transcribe", "--file=assets/雪-file.txt".encode("utf-8"), b"--format=json"]),
    ]
    cases = [(name, "transcribe " + typed, wanted, None, 10) for name, typed, wanted in cases]
    cases += [
        ("escaped colon command", r"chat\:comple", [b"chat:completions"],
         [b"__complete", b"--", b"chat:comple"], 0),
        ("quoted colon command", "'chat:comple'", [b"chat:completions"],
         [b"__complete", b"--", b"chat:comple"], 0),
        ("unquoted colon command", "chat:comple", [b"chat:completions"],
         [b"__complete", b"--", b"chat:comple"], 0),
        ("literal colon flag value", "--project : chat:completions create --moda",
         [b"--project", b":", b"chat:completions", b"create", b"--modality"],
         [b"__complete", b"--", b"--project", b":", b"chat:completions", b"create", b"--moda"], 0),
        ("separate colon command words", "chat: comple", [b"chat:", b"comple"],
         [b"__complete", b"--", b"chat:", b"comple"], 11),
    ]
    with tempfile.TemporaryDirectory(prefix="openai-bash-completion-") as temporary:
        root = pathlib.Path(temporary)
        os.chdir(root)
        env = {
            "PATH": "/usr/bin:/bin", "HOME": temporary, "XDG_CONFIG_HOME": temporary,
            "INPUTRC": "/dev/null", "HISTFILE": "/dev/null", "TERM": "xterm",
            "LC_ALL": "C", "CI": "1", "OPENAI_PICKER_SHELL": "off",
            "OPENAI_BASE_URL": "http://127.0.0.1:1", "BASH_SILENCE_DEPRECATION_WARNING": "1",
            "COMPLETION_BINARY": binary, "COMPLETION_ROOT": temporary,
        }
        version = harness.capture_fixture([bash, "--version"], env=env, timeout=5)
        assert version.returncode == 0 and not version.stderr, version.stderr
        print(version.stdout.decode("utf-8").splitlines()[0], flush=True)
        emitted = harness.capture_fixture([binary, "@completion", "bash"], env=env, timeout=5)
        assert emitted.returncode == 0 and not emitted.stderr, emitted.stderr
        (root / "completion.bash").write_bytes(emitted.stdout)
        (root / "assets").mkdir()
        (root / "assets" / "fixture name.json").write_bytes(b"")
        (root / "assets" / "quote's recording.wav").write_bytes(b"")
        (root / "assets" / "雪-file.txt").write_bytes(b"")
        (root / "assets" / "unrelated.txt").write_bytes(b"")
        (root / "startup.bash").write_text(r'''
PS1='COMPLETION_TEST> '
set +o history
bind 'set bell-style none'
cd "$COMPLETION_ROOT" || exit 90
completion_record_locale() {
  printf '%s\0' "${LC_ALL-}" "${LC_CTYPE-}" "${LANG-}"
}
completion_record_locale > "$COMPLETION_ROOT/startup.locale" || exit 94
if [[ ${COMPLETION_VERIFY_UTF8-} == 1 ]]; then
  builtin readonly LC_ALL || exit 94
  completion_charmap=$(locale charmap 2> "$COMPLETION_ROOT/locale.stderr") || exit 94
  printf '%s\n' "$completion_charmap" > "$COMPLETION_ROOT/locale.charmap" || exit 94
  completion_character='雪'
  printf '%s\0%s\0' "$completion_character" "${#completion_character}" > "$COMPLETION_ROOT/locale.character" || exit 94
  unset completion_character completion_charmap
fi
source "$COMPLETION_ROOT/completion.bash" || exit 91
openai() {
  case "${1-}" in
    __complete)
      completion_record_locale >> "$COMPLETION_ROOT/backend.locale" || return 92
      printf '%s\0' "$@" >> "$COMPLETION_ROOT/backend.argv" || return 92
      local status
      if "$COMPLETION_BINARY" "$@"; then status=0; else status=$?; fi
      printf '%s\n' "$status" >> "$COMPLETION_ROOT/backend.status" || return 92
      return "$status" ;;
    transcribe|chat:completions|chat:|--project)
      completion_record_locale > "$COMPLETION_ROOT/accepted.locale" || return 92
      printf '%s\0' "$@" > "$COMPLETION_ROOT/accepted.argv" || return 92
      printf '\nCOMPLETION_ENTERED\n' ;;
    *) return 93 ;;
  esac
}
printf '\nCOMPLETION_READY\n'
''', encoding="utf-8")
        for name, typed, wanted, expected_backend, expected_status in cases:
            unicode_case = name == "UTF-8 midline file with later flag"
            case_env = env
            if unicode_case:
                available = harness.capture_fixture(["locale", "-a"], env=env, timeout=5)
                assert available.returncode == 0 and not available.stderr, available.stderr
                locales = [value for value in available.stdout.decode("utf-8").splitlines()
                           if value.upper().replace("-", "").endswith("UTF8")]
                assert locales, "A UTF-8 locale is required for native midline completion"
                selected = next((value for value in locales
                                 if value.lower().replace("-", "") in ("c.utf8", "en_us.utf8")), locales[0])
                case_env = dict(env, LC_ALL=selected, LANG=selected, COMPLETION_VERIFY_UTF8="1")
            for filename in ("backend.argv", "backend.status", "accepted.argv", "startup.locale",
                             "backend.locale", "accepted.locale", "locale.charmap", "locale.stderr", "locale.character"):
                (root / filename).unlink(missing_ok=True)
            terminal = harness.Terminal(bash, ["--noprofile", "--norc", "-i"], case_env, controlling_terminal=True,
                                        max_output_bytes=1048576, max_event_bytes=1048576,
                                        max_events=16384, deadline=deadline, retain_child_identity=retain_identity)
            try:
                terminal.send(b'source "$COMPLETION_ROOT/startup.bash"\r')
                ready = b"\r\nCOMPLETION_READY\r\n"
                terminal.wait(ready.decode(), timeout=5)
                terminal.wait("COMPLETION_TEST> ", timeout=5, after=terminal.raw.index(ready) + len(ready))
                if unicode_case:
                    charmap = harness.read_fixture_file(root / "locale.charmap").strip().upper().replace(b"-", b"")
                    assert charmap == b"UTF8", (selected, charmap, terminal.text())
                    assert harness.read_fixture_file(root / "locale.character") == "雪\0".encode("utf-8") + b"1\0", terminal.text()
                before = b"\x02" * len(later_flag.encode("ascii")) if unicode_case else b""
                terminal.send(("openai " + typed).encode("utf-8") + before + b"\t")
                phase_deadline = min(deadline, time.monotonic() + 5)
                while not (root / "backend.status").exists():
                    terminal.read()
                    assert terminal.running() and time.monotonic() < phase_deadline, "completion backend did not finish"
                terminal.send((b"\x05" if unicode_case else b"") + b"\r")
                entered = b"\r\nCOMPLETION_ENTERED\r\n"
                terminal.wait(entered.decode(), timeout=5)
                terminal.wait("COMPLETION_TEST> ", timeout=5, after=terminal.raw.index(entered) + len(entered))
                terminal.send(b"exit\r")
                phase_deadline = min(deadline, time.monotonic() + 5)
                while terminal.running():
                    terminal.read()
                    assert time.monotonic() < phase_deadline, ("Bash did not exit", name, terminal.text())
                terminal.drain_eof()
                assert terminal.child.returncode == 0, terminal.text()
                accepted = harness.read_fixture_file(root / "accepted.argv")
                assert accepted == b"\0".join(wanted) + b"\0", (name, accepted, terminal.text())
                args = expected_backend
                if args is None:
                    value = "assets/fixture n" if name == "escaped assigned file" else "assets/fi"
                    if name == "double quoted apostrophe":
                        value = "assets/quote's"
                    if unicode_case:
                        value = "assets/雪-fi"
                    args = [b"__complete", b"--", b"transcribe"]
                    args += [b"--file", value.encode()] if name == "separate quoted file" else [("--file=" + value).encode()]
                assert harness.read_fixture_file(root / "backend.argv") == b"\0".join(args) + b"\0", name
                assert harness.read_fixture_file(root / "backend.status") == f"{expected_status}\n".encode("ascii"), name
                expected_locale = b"\0".join(case_env.get(key, "").encode("utf-8")
                                              for key in ("LC_ALL", "LC_CTYPE", "LANG")) + b"\0"
                for stage in ("startup", "backend", "accepted"):
                    actual_locale = harness.read_fixture_file(root / (stage + ".locale"))
                    assert actual_locale == expected_locale, (name, stage, actual_locale, expected_locale)
            finally:
                terminal.close()
            print("PASS: " + name, flush=True)


if __name__ == "__main__":
    def interrupted(signum, _frame):
        raise TimeoutError("completion fixture interrupted or exceeded60seconds")
    for selected_signal in (signal.SIGALRM, signal.SIGTERM, signal.SIGINT):
        signal.signal(selected_signal, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 60)
    try:
        main()
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
