#!/usr/bin/env python3
"""Check one explicit Bash setup lifecycle through three fresh private-PTY sessions."""
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import signal
import stat
import sys
import tempfile
import time


HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("picker_harness", HERE / "image_picker_harness.py")
picker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(picker)
PROMPT = "__PROFILE_READY__> "
DONE = "PROFILE_PROBE_DONE"
LIMIT = 1048576
SMALL = 65536
SYSTEM_STARTUP_FILES = (Path("/etc/profile"), Path("/etc/bash.bashrc"))


def system_source(path):
    """Snapshot bounded startup bytes and stable file identity without following a final symlink."""
    path = Path(path)
    record = {"path": str(path), "present": False}
    try:
        initial = path.lstat()
    except FileNotFoundError:
        return record

    def identity(info):
        return {"device": info.st_dev, "inode": info.st_ino, "mode": info.st_mode,
                "uid": info.st_uid, "gid": info.st_gid, "size": info.st_size,
                "mtime_ns": info.st_mtime_ns, "ctime_ns": info.st_ctime_ns}

    if not stat.S_ISREG(initial.st_mode) or initial.st_size > SMALL:
        raise RuntimeError("system startup source is not regular or exceeds64KiB: " + str(path))
    descriptor = os.open(path, os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0) | getattr(os, "O_NONBLOCK", 0))
    with os.fdopen(descriptor, "rb") as handle:
        opened = os.fstat(handle.fileno())
        if identity(opened) != identity(initial):
            raise RuntimeError("system startup source changed before read: " + str(path))
        data = handle.read(SMALL + 1)
        after_read = os.fstat(handle.fileno())
        if len(data) > SMALL or len(data) != opened.st_size:
            raise RuntimeError("system startup source changed size or exceeds64KiB: " + str(path))
        if identity(after_read) != identity(opened) or identity(path.lstat()) != identity(opened):
            raise RuntimeError("system startup source changed during read: " + str(path))
    record.update(present=True, bytes=len(data), sha256=hashlib.sha256(data).hexdigest(),
                  content_base64=base64.b64encode(data).decode("ascii"), identity=identity(opened))
    return record


def recheck_system_sources(before):
    after = [system_source(record["path"]) for record in before]
    return after, after == before


def main():
    if len(sys.argv) != 4 or sys.argv[3] not in ("login", "nonlogin"):
        raise SystemExit("usage: check-wsl-shell-startup.py CLI BASH login|nonlogin")
    binary, bash = (str(Path(value).resolve(strict=True)) for value in sys.argv[1:3])
    mode = sys.argv[3]
    name = "bash-" + mode
    deadline = time.monotonic() + 115
    root = Path(tempfile.mkdtemp(prefix="openai-wsl-startup-"))
    identity = root.stat()
    owned = []
    report = {"case": name, "exit_code": 1, "cleanup_complete": False,
              "setup_calls": [], "fresh_shells": [], "failures": [],
              "system_sources": [], "system_sources_after": [], "system_sources_unchanged": False}
    terminal = None

    def interrupted(signum, _frame):
        raise TimeoutError("startup fixture interrupted or exceeded115seconds")

    for selected_signal in (signal.SIGALRM, signal.SIGTERM, signal.SIGINT):
        signal.signal(selected_signal, interrupted)
    signal.setitimer(signal.ITIMER_REAL, 115)
    try:
        for source in SYSTEM_STARTUP_FILES:
            report["system_sources"].append(system_source(source))
        bindir = root / "bin"
        bindir.mkdir(mode=0o700)
        (bindir / "openai").symlink_to(binary)
        home = root / "home with ' quotes"
        home.mkdir(mode=0o700)
        config = home / "config with ' quotes"
        for path in (config, home / "tmp", home / "cache", home / "data"):
            path.mkdir(mode=0o700)
        originals = {}
        for profile in ("bashrc", "bash_profile"):
            content = ("# Synthetic owned startup\n"
                       "export OPENAI_PROFILE_TEST_SENTINEL=" + profile + "\n"
                       'export PATH="$OPENAI_PROFILE_BIN:$PATH"\n'
                       "PS1='" + PROMPT + "'\nHISTFILE=/dev/null\n").encode()
            path = home / ("." + profile)
            path.write_bytes(content)
            path.chmod(0o600)
            originals[path] = content
        other = home / ".profile"
        other.write_bytes(b"# Preserve this unselected owned profile.\n")
        other.chmod(0o600)
        originals[other] = other.read_bytes()
        managed = [home / ".bashrc", home / ".bash_profile"]
        env = {"PATH": str(bindir) + ":/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(home),
               "USERPROFILE": str(home), "XDG_CONFIG_HOME": str(config),
               "XDG_CACHE_HOME": str(home / "cache"), "XDG_DATA_HOME": str(home / "data"),
               "TMPDIR": str(home / "tmp"), "SHELL": bash, "TERM": "xterm-256color",
               "LANG": "C.UTF-8", "LC_ALL": "C.UTF-8", "INPUTRC": "/dev/null", "CI": "true",
               "GOMAXPROCS": "2", "OPENAI_PROFILE_BIN": str(bindir),
               "OPENAI_BASE_URL": "http://127.0.0.1:1"}
        selected_locale, locale_source = picker.fixture_utf8_locale(os.environ.get("LC_ALL"), env, owned)
        env.update(LANG=selected_locale, LC_ALL=selected_locale)
        retain_identity = sys.platform.startswith("linux") and all(
            hasattr(os, key) for key in ("waitid", "WNOWAIT", "P_PID"))
        report.update(locale=selected_locale, locale_source=locale_source,
                      retained_child_identity=retain_identity)

        def admit(seconds):
            if time.monotonic() + seconds > deadline:
                raise TimeoutError("insufficient startup time for the complete next action")

        def snapshot():
            result = {}
            for path in managed:
                data = picker.read_fixture_file(path, SMALL)
                info = path.stat()
                result[path.name] = (data, info.st_dev, info.st_ino, info.st_mtime_ns,
                                    info.st_mode, info.st_size)
            return result

        def original_profiles():
            for path, expected in originals.items():
                assert picker.read_fixture_file(path, SMALL) == expected, "owned user profile changed"

        def no_generated_scripts():
            count = 0
            for directory, subdirs, files in os.walk(home, followlinks=False):
                count += len(subdirs) + len(files)
                assert count <= 256, "owned profile inventory exceeded its bound"
                assert not any(value.startswith("picker-bash-") for value in files + subdirs), directory

        def setup(label, action):
            admit(10)
            got = picker.capture_fixture([binary, "@completion", "bash", action], env=env,
                                         cwd=home, timeout=10, limit=LIMIT, owned_processes=owned)
            report["setup_calls"].append({"action": label, "exit_code": got.returncode,
                                          "stdout_eof": got.stdout_eof, "stderr_eof": got.stderr_eof,
                                          "stdout_bytes": len(got.stdout), "stderr_bytes": len(got.stderr),
                                          "stdout_base64": base64.b64encode(got.stdout).decode("ascii"),
                                          "stderr_base64": base64.b64encode(got.stderr).decode("ascii"),
                                          "stderr_sha256": hashlib.sha256(got.stderr).hexdigest()})
            message = ("Tab setup saved for future terminals. Existing custom bindings are preserved.\n"
                       if action == "--install-picker" else
                       "Tab shortcut setup removed. Open a new terminal to finish.\n")
            assert got.returncode == 0 and got.stdout == b"" and got.stderr == message.encode(), label
            assert picker.read_fixture_file(other, SMALL) == originals[other], "unselected profile changed"

        def fresh(label, active):
            nonlocal terminal
            admit(30)
            until = min(deadline, time.monotonic() + 30)
            args = ["--login", "-i"] if mode == "login" else ["-i"]
            terminal = picker.Terminal(bash, args, env, controlling_terminal=True,
                                       max_output_bytes=LIMIT, max_event_bytes=LIMIT,
                                       max_events=16384, deadline=until,
                                       retain_child_identity=retain_identity, cwd=home)
            item = {"checkpoint": label, "expected_active": active, "exit_code": None,
                    "pty_eof": False, "forced_cleanup": False}
            report["fresh_shells"].append(item)
            try:
                terminal.wait(PROMPT, timeout=20)
                item["first_prompt_offset"] = terminal.raw.index(PROMPT.encode())
                command = ("printf 'SENTINEL:%s\\n' \"${OPENAI_PROFILE_TEST_SENTINEL-}\"; "
                           "printf 'EXECUTABLE:%s\\n' \"$(command -v openai)\"; "
                           "printf 'VERSION:%s\\n' \"$BASH_VERSION\"; "
                           "printf 'INTEGRATION:%s\\n' \"${OPENAI_PICKER_INTEGRATION-}\"; "
                           "complete -p openai 2>/dev/null || :; bind -m emacs-standard -s; "
                           "printf 'PROFILE_%s\\n' PROBE_DONE; IFS= read -r profile_ack; "
                           "test \"$profile_ack\" = finish && exit 0")
                assert DONE not in command
                offset = len(terminal.raw)
                item["probe_start_offset"] = offset
                terminal.send(command.encode() + b"\r")
                terminal.wait(DONE, timeout=5, after=offset)
                item["probe_marker_offset"] = terminal.raw.index(DONE.encode(), offset)
                assert terminal.running(), "shell exited before acknowledgement"
                lines = terminal.text().replace("\r\n", "\n").splitlines()
                sentinel = "bash_profile" if mode == "login" else "bashrc"
                assert "SENTINEL:" + sentinel in lines, "wrong startup profile"
                assert "EXECUTABLE:" + str(bindir / "openai") in lines, "wrong executable"
                versions = [line[len("VERSION:"):] for line in lines if line.startswith("VERSION:")]
                assert len(versions) == 1 and versions[0], "missing native Bash version"
                item["bash_version"] = versions[0]
                assert "INTEGRATION:" + ("bash" if active else "") in lines, "wrong integration state"
                completion = "complete -o filenames -F __openai_picker_complete openai"
                binding = '"\\C-i": "\\e[99;1\\C-i\\e[99;2~"'
                assert (completion in lines) == active, "wrong completion registration"
                assert (binding in lines) == active, "wrong managed Tab binding"
                terminal.send(b"finish\r")
                while terminal.running():
                    terminal.read()
                terminal.drain_eof()
                assert terminal.child.returncode == 0, "fresh shell failed"
                item.update(exit_code=terminal.child.returncode, pty_eof=terminal.eof)
            finally:
                item["forced_cleanup"] = terminal.child.returncode is None
                item["pty_bytes"] = len(terminal.raw)
                item["pty_sha256"] = hashlib.sha256(terminal.raw).hexdigest()
                item["pty_base64"] = base64.b64encode(terminal.raw).decode("ascii")
                item["event_count"] = len(terminal.events)
                terminal.close()
                terminal = None

        setup("install", "--install-picker")
        installed = snapshot()
        for path in managed:
            data = installed[path.name][0]
            assert data.startswith(originals[path]) and data.count(b"# >>> openai image picker") == 1
        setup("install-again", "--install-picker")
        assert snapshot() == installed, "repeated installation changed bytes or identity"
        fresh("fresh-installed", True)
        marker = home / ".openai/shell/image-picker.tab-off-bash"
        setup("remove", "--uninstall-picker")
        original_profiles()
        assert picker.read_fixture_file(marker, SMALL) == b""
        no_generated_scripts()
        setup("remove-again", "--uninstall-picker")
        original_profiles()
        assert picker.read_fixture_file(marker, SMALL) == b""
        fresh("fresh-removed", False)
        setup("reenable", "--install-picker")
        assert not marker.exists() and not marker.is_symlink(), "explicit re-enable retained opt-out"
        fresh("fresh-reenabled", True)
        setup("remove-final", "--uninstall-picker")
        original_profiles()
        assert picker.read_fixture_file(marker, SMALL) == b""
        no_generated_scripts()
        assert len(report["setup_calls"]) == 6 and len(report["fresh_shells"]) == 3
        assert all(item["pty_eof"] and not item["forced_cleanup"] for item in report["fresh_shells"])
        assert time.monotonic() < deadline, "startup lifecycle exceeded115seconds"
        report["exit_code"] = 0
    except BaseException as error:
        report["failures"].append(str(error)[:2000])
    finally:
        try:
            after, unchanged = recheck_system_sources(report["system_sources"])
            report["system_sources_after"] = after
            report["system_sources_unchanged"] = unchanged and len(after) == len(SYSTEM_STARTUP_FILES)
            if not report["system_sources_unchanged"]:
                raise RuntimeError("system startup sources changed or the initial snapshot was incomplete")
            if time.monotonic() >= deadline:
                raise TimeoutError("system source recheck exceeded the startup deadline")
        except BaseException as error:
            report["exit_code"] = 1
            report["failures"].append("system source recheck: " + str(error)[:2000])
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        try:
            if terminal is not None:
                terminal.close()
            for process in owned:
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                process.wait(timeout=2)
                assert process.fixture_stdout_eof and process.fixture_stderr_eof, "setup pipe EOF was not observed"
            info = root.lstat()
            assert stat.S_ISDIR(info.st_mode) and (info.st_dev, info.st_ino) == (identity.st_dev, identity.st_ino)
            shutil.rmtree(root)
            assert not root.exists()
            report["cleanup_complete"] = True
        except BaseException as error:
            report["exit_code"] = 1
            report["failures"].append("cleanup: " + str(error)[:2000])
    report.update(setup_call_count=len(report["setup_calls"]), fresh_shell_count=len(report["fresh_shells"]),
                  expected_setup_calls=6, expected_fresh_shells=3, request_count=None,
                  request_count_scope="Not measured; these are local setup commands.",
                  setup_stdout_eof=len(report["setup_calls"]) == 6 and all(x["stdout_eof"] for x in report["setup_calls"]),
                  setup_stderr_eof=len(report["setup_calls"]) == 6 and all(x["stderr_eof"] for x in report["setup_calls"]),
                  pty_eof=len(report["fresh_shells"]) == 3 and all(x["pty_eof"] for x in report["fresh_shells"]))
    report["cleanup_scope"] = "Owned direct children, observed pipe/PTY EOF, and private root; no complete descendant census."
    report["system_source_scope"] = "Two startup entrypoint snapshots; no transitive-source completeness or arbitrary-code safety claim."
    encoded = json.dumps(report, separators=(",", ":"))
    assert len(encoded.encode()) <= 8 * LIMIT, "startup receipt exceeded its bound"
    print("RECEIPT: " + encoded, flush=True)
    if report["exit_code"] == 0 and report["cleanup_complete"]:
        print("PASS: " + name, flush=True)
        return 0
    print("FAIL: " + name, file=sys.stderr, flush=True)
    return 1


if __name__ == "__main__":
    raise SystemExit(main())
