#!/usr/bin/env python3
"""Check one supported or refused Bash setup lifecycle through three private-PTY sessions."""
import argparse
from contextlib import ExitStack
import base64
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
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
REFUSAL_GUIDANCE = (
    " Type openai images generate and press Enter to use the picker."
    " Some startup files or preferences may already have changed."
    " Keep any .openai-picker-recovery-* directories beside the startup file; inspect their original files before manual recovery."
    " Persistent setup needs a filesystem with atomic rename support."
)


def refusal_message(action):
    if action == "--install-picker":
        return "Could not finish Tab shortcut setup: this environment does not support the required atomic rename." + REFUSAL_GUIDANCE
    assert action == "--uninstall-picker", "unknown refused setup action"
    return ("Could not finish Tab shortcut removal: this environment does not support the required atomic rename."
            " Existing shortcuts may remain active." + REFUSAL_GUIDANCE)


def unique_object(pairs):
    result = dict(pairs)
    assert len(result) == len(pairs), "duplicate fixture JSON key"
    return result


def require_setup_result(got, action, refused=False, error_format="text"):
    assert got.stdout_eof and got.stderr_eof, "setup pipe EOF was not observed"
    assert got.stdout == b"", "setup wrote unexpected stdout"
    assert got.returncode == (1 if refused else 0), "setup exit status differed"
    if refused:
        message = refusal_message(action)
        if error_format == "json":
            assert json.loads(got.stderr.decode("utf-8"), object_pairs_hook=unique_object) == {"message": message}
        else:
            assert got.stderr == (message + "\n").encode(), "setup refusal message differed"
    else:
        message = ("Tab setup saved for future terminals. Existing custom bindings are preserved.\n"
                   if action == "--install-picker" else
                   "Tab shortcut setup removed. Open a new terminal to finish.\n")
        assert got.stderr == message.encode(), "setup receipt differed"


def exported_fixtures(got, directory, profiles):
    assert got.returncode == 0 and got.stderr == b"", "startup fixture exporter failed"
    assert got.stdout_eof and got.stderr_eof, "startup fixture exporter pipe EOF was not observed"
    assert len(got.stdout) <= LIMIT, "startup fixture exporter exceeded its bound"
    lines = got.stdout.decode("utf-8").splitlines()
    assert len(lines) == 4 and lines[0] == "=== RUN   TestPickerStartupFixtureExport"
    assert re.fullmatch(r"--- PASS: TestPickerStartupFixtureExport \([0-9.]+s\)", lines[2])
    assert lines[3] == "PASS" and lines[1].startswith("PICKER_STARTUP_FIXTURE ")
    value = json.loads(lines[1][len("PICKER_STARTUP_FIXTURE "):], object_pairs_hook=unique_object)
    assert set(value) == {"contract", "shell", "directory", "fixtures"}, "unexpected exporter fields"
    assert value["contract"] == "seeded-existing-state" and value["shell"] == "bash"
    assert value["directory"] == str(directory), "exporter changed the owned script directory"
    fixtures = value["fixtures"]
    assert isinstance(fixtures, list) and len(fixtures) == len(profiles), "exporter profile count differed"
    for fixture, profile in zip(fixtures, profiles):
        assert set(fixture) == {"profile", "script_name", "script", "block"}, "unexpected fixture fields"
        assert fixture["profile"] == str(profile), "exporter changed the owned profile"
        for field, limit in (("script", LIMIT), ("block", SMALL)):
            assert isinstance(fixture[field], str) and 0 < len(fixture[field].encode()) <= limit
        script_hash = hashlib.sha256(fixture["script"].encode()).hexdigest()
        profile_hash = hashlib.sha256(str(profile).encode()).hexdigest()[:16]
        assert fixture["script_name"] == f"picker-bash-{profile_hash}-{script_hash}.bash", "exporter script identity differed"
        assert fixture["block"].count("# >>> openai image picker") == 1, "exporter block marker differed"
    return fixtures


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
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cli")
    parser.add_argument("bash")
    parser.add_argument("mode", choices=("login", "nonlogin"))
    parser.add_argument("--setup-contract", choices=("supported", "refused"), default="supported")
    parser.add_argument("--autocomplete-test-binary")
    options = parser.parse_args()
    if options.setup_contract == "refused" and not options.autocomplete_test_binary:
        parser.error("--setup-contract refused requires --autocomplete-test-binary")
    binary, bash = (str(Path(value).resolve(strict=True)) for value in (options.cli, options.bash))
    exporter = str(Path(options.autocomplete_test_binary).resolve(strict=True)) if options.autocomplete_test_binary else None
    mode, contract = options.mode, options.setup_contract
    name = "bash-" + mode + ("-refused" if contract == "refused" else "")
    expected_calls = 7 if contract == "refused" else 6
    deadline = time.monotonic() + 115
    root = Path(tempfile.mkdtemp(prefix="openai-wsl-startup-"))
    identity = root.stat()
    owned = []
    report = {"case": name, "setup_contract": contract, "exit_code": 1, "cleanup_complete": False,
              "setup_calls": [], "fresh_shells": [], "failures": [],
              "system_sources": [], "system_sources_after": [], "system_sources_unchanged": False}
    terminal = None

    def interrupted(signum, _frame):
        raise TimeoutError("startup fixture interrupted or exceeded115seconds")

    handlers = {}
    try:
        for selected_signal in (signal.SIGALRM, signal.SIGTERM, signal.SIGINT):
            handlers[selected_signal] = signal.signal(selected_signal, interrupted)
        signal.setitimer(signal.ITIMER_REAL, 115)
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

        def snapshot(paths=None, limit=SMALL):
            result = {}
            for path in (managed if paths is None else paths):
                data = picker.read_fixture_file(path, limit)
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

        def setup(label, action, refused=False, error_format="text"):
            admit(10)
            command = [binary]
            if error_format == "json":
                command.extend(["--format-error", "json"])
            command.extend(["@completion", "bash", action])
            got = picker.capture_fixture(command, env=env,
                                         cwd=home, timeout=10, limit=LIMIT, owned_processes=owned)
            report["setup_calls"].append({"action": label, "exit_code": got.returncode,
                                          "expected_exit_code": 1 if refused else 0, "format_error": error_format,
                                          "stdout_eof": got.stdout_eof, "stderr_eof": got.stderr_eof,
                                          "stdout_bytes": len(got.stdout), "stderr_bytes": len(got.stderr),
                                          "stdout_base64": base64.b64encode(got.stdout).decode("ascii"),
                                          "stderr_base64": base64.b64encode(got.stderr).decode("ascii"),
                                          "stderr_sha256": hashlib.sha256(got.stderr).hexdigest()})
            require_setup_result(got, action, refused, error_format)
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
                picker.cleanup_fixture_resources(terminal.close)
                terminal = None

        marker = home / ".openai/shell/image-picker.tab-off-bash"
        if contract == "refused":
            report["installation_scope"] = "Seeded existing state tests activation and cleanup, not installation."
            report["automatic_setup_scope"] = "Not exercised: private HOME differs from the account home."

            def write_new(path, data):
                descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
                with os.fdopen(descriptor, "wb") as handle:
                    handle.write(data)

            # A completed synthetic archive must survive every setup/removal attempt.
            archive_hash = hashlib.sha256(b".bashrc").hexdigest()[:16]
            archive = home / (".openai-picker-recovery-" + archive_hash + "-fixture")
            archive.mkdir(mode=0o700)
            write_new(archive / "manifest.json", b'{"version":1,"profile":".bashrc"}')
            write_new(archive / "original", b"# Synthetic archived startup; retain for manual recovery.\n")
            write_new(archive / "complete", b"")
            preserved_paths = [other, archive / "manifest.json", archive / "original", archive / "complete"]
            preserved_before = [system_source(path) for path in preserved_paths]

            def preserved():
                assert [system_source(path) for path in preserved_paths] == preserved_before, "unselected profile or recovery evidence changed"

            original_snapshot = snapshot()
            for label, error_format in (("install-refused-text", "text"), ("install-refused-json", "json"),
                                        ("install-refused-again", "text")):
                setup(label, "--install-picker", refused=True, error_format=error_format)
                assert snapshot() == original_snapshot, "refused installation changed startup bytes or identity"
                no_generated_scripts()
                preserved()
            report["original_profiles_unchanged"] = True
            fresh("fresh-original", False)

            directory = config / "openai/shell"
            request = json.dumps({"directory": str(directory), "profiles": [str(path) for path in managed]})
            admit(10)
            exported = picker.capture_fixture([exporter, "-test.run", "^TestPickerStartupFixtureExport$", "-test.v"],
                                               env={**env, "OPENAI_CLI_PICKER_FIXTURE_REQUEST": request},
                                               cwd=home, timeout=10, limit=LIMIT, owned_processes=owned)
            fixtures = exported_fixtures(exported, directory, managed)
            assert snapshot() == original_snapshot, "fixture exporter changed startup files"
            no_generated_scripts()
            preserved()
            for path in (directory.parent, directory):
                if not path.exists():
                    path.mkdir(mode=0o700)
                info = path.lstat()
                assert stat.S_ISDIR(info.st_mode) and not stat.S_IMODE(info.st_mode) & 0o077, "unsafe owned fixture directory"
            scripts = []
            for fixture, profile in zip(fixtures, managed):
                script = directory / fixture["script_name"]
                write_new(script, fixture["script"].encode())
                with profile.open("ab") as handle:
                    handle.write(fixture["block"].encode())
                scripts.append(script)
            report["seeded_existing_state"] = {
                "contract": "seeded-existing-state", "exporter_exit_code": exported.returncode,
                "stdout_eof": exported.stdout_eof, "stderr_eof": exported.stderr_eof,
                "exporter_stdout_bytes": len(exported.stdout), "exporter_stderr_bytes": len(exported.stderr),
                "exporter_stdout_sha256": hashlib.sha256(exported.stdout).hexdigest(),
                "profile_count": len(managed), "script_count": len(scripts),
            }
            seeded_snapshot = snapshot()
            scripts_before = snapshot(scripts, LIMIT)

            def seeded_unchanged():
                assert snapshot() == seeded_snapshot, "seeded startup bytes or identity changed"
                assert snapshot(scripts, LIMIT) == scripts_before, "seeded script bytes or identity changed"
                preserved()

            setup("seeded-install-noop", "--install-picker")
            seeded_unchanged()
            assert not marker.exists() and not marker.is_symlink(), "unchanged install retained opt-out"
            fresh("fresh-seeded-existing", True)
            for label, error_format in (("seeded-remove-refused-text", "text"), ("seeded-remove-refused-json", "json")):
                setup(label, "--uninstall-picker", refused=True, error_format=error_format)
                seeded_unchanged()
                assert picker.read_fixture_file(marker, SMALL) == b"", "refused removal lost explicit opt-out"
            fresh("fresh-after-refused-remove", True)
            seeded_unchanged()
            report["seeded_profiles_unchanged"] = True

            # Only the harness removes these owned fixtures; this is not successful profile replacement.
            for path in managed:
                path.unlink()
            setup("absent-profile-remove", "--uninstall-picker")
            assert all(not path.exists() and not path.is_symlink() for path in managed), "cleanup recreated an absent profile"
            assert picker.read_fixture_file(marker, SMALL) == b"", "absent-profile cleanup lost opt-out"
            no_generated_scripts()
            preserved()
            report.update(absent_profiles_preserved=True, opt_out_created=True, generated_scripts_removed=True,
                          unselected_profile_unchanged=True, recovery_evidence_unchanged=True,
                          recovery_evidence_scope="Completed synthetic archive with retained original bytes.")
        else:
            setup("install", "--install-picker")
            installed = snapshot()
            for path in managed:
                data = installed[path.name][0]
                assert data.startswith(originals[path]) and data.count(b"# >>> openai image picker") == 1
            setup("install-again", "--install-picker")
            assert snapshot() == installed, "repeated installation changed bytes or identity"
            fresh("fresh-installed", True)
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
        assert len(report["setup_calls"]) == expected_calls and len(report["fresh_shells"]) == 3
        assert all(item["pty_eof"] and not item["forced_cleanup"] for item in report["fresh_shells"])
        assert time.monotonic() < deadline, "startup lifecycle exceeded115seconds"
        report["exit_code"] = 0
    except Exception as error:
        report["failures"].append(str(error)[:2000])
    finally:
        cleanup_incomplete = False

        def cleanup(label, action, *args, blocks_removal=True):
            nonlocal cleanup_incomplete
            complete = False
            try:
                action(*args)
                complete = True
            except Exception as error:
                report["exit_code"] = 1
                report["failures"].append(label + ": " + str(error)[:2000])
            finally:
                # Control exceptions propagate through ExitStack after later cleanup callbacks.
                if not complete and blocks_removal:
                    cleanup_incomplete = True

        def recheck_sources():
            after, unchanged = recheck_system_sources(report["system_sources"])
            report["system_sources_after"] = after
            report["system_sources_unchanged"] = unchanged and len(after) == len(SYSTEM_STARTUP_FILES)
            if not report["system_sources_unchanged"]:
                raise RuntimeError("system startup sources changed or the initial snapshot was incomplete")
            if time.monotonic() >= deadline:
                raise TimeoutError("system source recheck exceeded the startup deadline")

        def close_child(process):
            def stop_child():
                if process.poll() is None:
                    try:
                        os.killpg(process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        # The owned group exited; still reap the child and verify pipe EOF.
                        pass

            def check_pipes():
                assert process.fixture_stdout_eof and process.fixture_stderr_eof, "setup pipe EOF was not observed"
                assert getattr(process, "fixture_cleanup_complete", True), "fixture pipe cleanup was incomplete"

            with ExitStack() as child_cleanup:
                child_cleanup.callback(cleanup, "child pipes", check_pipes)
                child_cleanup.callback(cleanup, "child wait", lambda: process.wait(timeout=2))
                child_cleanup.callback(cleanup, "child stop", stop_child)

        def remove_root():
            if not cleanup_incomplete:
                info = root.lstat()
                assert stat.S_ISDIR(info.st_mode) and (info.st_dev, info.st_ino) == (identity.st_dev, identity.st_ino)
                shutil.rmtree(root)
                assert not root.exists()
                report["cleanup_complete"] = True

        with ExitStack() as cleanup_stack:
            # Callbacks run in reverse order. Register all phases before running any cleanup.
            for selected_signal, previous in handlers.items():
                cleanup_stack.callback(cleanup, "signal restoration", signal.signal, selected_signal, previous, blocks_removal=False)
            cleanup_stack.callback(cleanup, "root cleanup", remove_root)
            for process in reversed(owned):
                cleanup_stack.callback(cleanup, "child cleanup", close_child, process)
            if terminal is not None:
                cleanup_stack.callback(cleanup, "terminal cleanup", terminal.close)
            cleanup_stack.callback(cleanup, "system source recheck", recheck_sources, blocks_removal=False)
            cleanup_stack.callback(cleanup, "alarm cancellation", signal.setitimer, signal.ITIMER_REAL, 0)
    report.update(setup_call_count=len(report["setup_calls"]), fresh_shell_count=len(report["fresh_shells"]),
                  expected_setup_calls=expected_calls, expected_fresh_shells=3, request_count=None,
                  request_count_scope="Not measured; these are local setup commands.",
                  setup_stdout_eof=len(report["setup_calls"]) == expected_calls and all(x["stdout_eof"] for x in report["setup_calls"]),
                  setup_stderr_eof=len(report["setup_calls"]) == expected_calls and all(x["stderr_eof"] for x in report["setup_calls"]),
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
