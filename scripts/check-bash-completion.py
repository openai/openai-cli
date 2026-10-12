#!/usr/bin/env python3
"""Check native Bash Readline against a built CLI without sending API requests."""
import importlib.util
import os
import pathlib
import subprocess
import sys
import tempfile
import time


def main():
    binary, bash = (str(pathlib.Path(arg).resolve()) for arg in sys.argv[1:])
    spec = importlib.util.spec_from_file_location(
        "terminal_harness", pathlib.Path(__file__).with_name("image_picker_harness.py"))
    harness = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(harness)
    assigned = [b"transcribe", b"--file=assets/fixture name.json"]
    cases = [
        ("single quoted assigned file", "--file='assets/fi'", assigned),
        ("double quoted assigned file", '--file="assets/fi"', assigned),
        ("escaped assigned file", r"--file=assets/fixture\ n", assigned),
        ("separate quoted file", '--file "assets/fi"', [b"transcribe", b"--file", b"assets/fixture name.json"]),
        ("unquoted assigned control", "--file=assets/fi", assigned),
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
        version = subprocess.run([bash, "--version"], env=env, capture_output=True, text=True, timeout=5, check=True)
        print(version.stdout.splitlines()[0], flush=True)
        emitted = subprocess.run([binary, "@completion", "bash"], env=env, capture_output=True, timeout=5, check=True)
        assert not emitted.stderr, emitted.stderr
        (root / "completion.bash").write_bytes(emitted.stdout)
        (root / "assets").mkdir()
        (root / "assets" / "fixture name.json").write_bytes(b"")
        (root / "startup.bash").write_text(r'''
PS1='COMPLETION_TEST> '
set +o history
bind 'set bell-style none'
cd "$COMPLETION_ROOT" || exit 90
source "$COMPLETION_ROOT/completion.bash" || exit 91
openai() {
  case "${1-}" in
    __complete)
      printf '%s\0' "$@" >> "$COMPLETION_ROOT/backend.argv" || return 92
      local status
      if "$COMPLETION_BINARY" "$@"; then status=0; else status=$?; fi
      printf '%s\n' "$status" >> "$COMPLETION_ROOT/backend.status" || return 92
      return "$status" ;;
    transcribe)
      printf '%s\0' "$@" > "$COMPLETION_ROOT/accepted.argv" || return 92
      printf '\nCOMPLETION_ENTERED\n' ;;
    *) return 93 ;;
  esac
}
printf '\nCOMPLETION_READY\n'
''')
        for name, typed, wanted in cases:
            for filename in ("backend.argv", "backend.status", "accepted.argv"):
                (root / filename).unlink(missing_ok=True)
            terminal = harness.Terminal(bash, ["--noprofile", "--norc", "-i"], env, controlling_terminal=True)
            try:
                terminal.send(b'source "$COMPLETION_ROOT/startup.bash"\r')
                ready = b"\r\nCOMPLETION_READY\r\n"
                terminal.wait(ready.decode(), timeout=5)
                terminal.wait("COMPLETION_TEST> ", timeout=5, after=terminal.raw.index(ready) + len(ready))
                terminal.send(("openai transcribe " + typed).encode() + b"\t")
                deadline = time.monotonic() + 5
                while not (root / "backend.status").exists():
                    terminal.read()
                    assert terminal.child.poll() is None and time.monotonic() < deadline, "completion backend did not finish"
                terminal.send(b"\r")
                entered = b"\r\nCOMPLETION_ENTERED\r\n"
                terminal.wait(entered.decode(), timeout=5)
                terminal.wait("COMPLETION_TEST> ", timeout=5, after=terminal.raw.index(entered) + len(entered))
                terminal.send(b"exit\r")
                deadline = time.monotonic() + 5
                while terminal.child.poll() is None:
                    terminal.read()
                    assert time.monotonic() < deadline, ("Bash did not exit", name, terminal.text())
                assert terminal.child.returncode == 0, terminal.text()
                accepted = (root / "accepted.argv").read_bytes()
                assert accepted == b"\0".join(wanted) + b"\0", (name, accepted, terminal.text())
                value = "assets/fixture n" if name == "escaped assigned file" else "assets/fi"
                args = [b"__complete", b"--", b"transcribe"]
                args += [b"--file", value.encode()] if name == "separate quoted file" else [("--file=" + value).encode()]
                assert (root / "backend.argv").read_bytes() == b"\0".join(args) + b"\0", name
                assert (root / "backend.status").read_bytes() == b"10\n", name
            finally:
                terminal.close()
            print("PASS: " + name, flush=True)


if __name__ == "__main__":
    main()
