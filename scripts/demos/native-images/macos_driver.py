"""PROTOTYPE: native captures only on an explicitly approved GitHub-hosted Mac.

No local fallback, privacy-setting changes, input injection, or API image calls.
The scene is the reviewed Linux scene with its platform guard generalized.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import shlex
import signal
import struct
import subprocess
import sys
import time
import urllib.request

SOURCE = Path(__file__).resolve().parent
RUNTIMES = {
    "kitty": {
        "url": "https://github.com/kovidgoyal/kitty/releases/download/v0.49.1/kitty-0.49.1.dmg",
        "sha256": "d258b6dcab1866a9bc456c55b3754c6cdf6a7a6a93d038475275675bb9f2053a",
        "bundle": "kitty.app", "executable": "kitty", "id": "net.kovidgoyal.kitty", "version": "0.49.1",
    },
    "ghostty": {
        "url": "https://release.files.ghostty.org/1.3.1/Ghostty.dmg",
        "sha256": "18cff2b0a6cee90eead9c7d3064e808a252a40baf214aa752c1ecb793b8f5f69",
        "bundle": "Ghostty.app", "executable": "ghostty", "id": "com.mitchellh.ghostty", "version": "1.3.1",
    },
}


class SceneCleanupError(RuntimeError):
    """The disposable runner must stop rather than launch another terminal."""


def check(ok, message):
    if not ok:
        raise RuntimeError(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def record(path, data):
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def run(argv, *, timeout=30, env=None):
    return subprocess.run([str(x) for x in argv], stdin=subprocess.DEVNULL,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                          timeout=timeout, check=True, env=env)


def bounded_output(data, limit=8192):
    data = data or b""
    if not isinstance(data, bytes):
        data = str(data).encode()
    return {"text": data[:limit].decode(errors="replace"), "bytes": len(data),
            "truncated": len(data) > limit}


def failure_details(error):
    return {"type": type(error).__name__, "message": str(error)[:4096],
            "returncode": getattr(error, "returncode", None),
            "stdout": bounded_output(getattr(error, "stdout", None)),
            "stderr": bounded_output(getattr(error, "stderr", None))}


def log_tail(path, limit=8192):
    if not path.exists():
        return {"missing": True}
    with path.open("rb") as stream:
        size = stream.seek(0, os.SEEK_END)
        stream.seek(max(0, size - limit))
        data = stream.read(limit)
    return {"text": data.decode(errors="replace"), "bytes": size, "truncated": size > limit}


def png_info(path):
    data = path.read_bytes()
    check(len(data) > 32 and data[:8] == b"\x89PNG\r\n\x1a\n" and data[12:16] == b"IHDR", "invalid native PNG")
    width, height = struct.unpack("!II", data[16:24])
    check(width >= 200 and height >= 100, "capture dimensions too small")
    return {"sha256": hashlib.sha256(data).hexdigest(), "bytes": len(data), "width": width, "height": height}


def download(runtime, path):
    digestor = hashlib.sha256()
    total = 0
    with urllib.request.urlopen(runtime["url"], timeout=30) as response, path.open("xb") as out:
        while chunk := response.read(1024 * 1024):
            total += len(chunk)
            check(total <= 256 * 1024 * 1024, "runtime download exceeds 256MiB")
            digestor.update(chunk)
            out.write(chunk)
    check(digestor.hexdigest() == runtime["sha256"], "pinned runtime digest mismatch")


def wait_ready(path, process, seconds):
    deadline = time.monotonic() + seconds
    while not path.exists():
        check(process.poll() is None, "terminal exited before scene checkpoint")
        check(time.monotonic() < deadline, "scene checkpoint timed out")
        time.sleep(0.05)
    return json.loads(path.read_text())


def terminate_owned_scene(directory, process):
    # Request cleanup from the scene that still owns the actual child Popen.
    # Never signal a numeric process-group ID read from a possibly stale file.
    (directory / "cancel-requested").write_text("capture driver finished or failed\n")
    forced = False
    try:
        process.wait(timeout=8)
    except subprocess.TimeoutExpired:
        forced = True
        process.terminate()
        try:
            process.wait(timeout=3)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=2)
    return {"terminal_reaped": True, "forced_terminal_shutdown": forced,
            "scene_group_marker_remaining": (directory / "active-group").exists()}


def one_case(root, terminal, executable, window_tool, label, mode):
    # Layout is consumed by the reviewed scene's strict directory check.
    case = root / "runs" / ("run-" + terminal) / (mode + "-" + label)
    case.mkdir(parents=True)
    config = case / "terminal-config"
    config.mkdir()
    home = case / "terminal-home"
    home.mkdir()
    env = {"PATH": "/usr/bin:/bin:/usr/sbin:/sbin", "HOME": str(home),
           "XDG_CONFIG_HOME": str(config), "XDG_CACHE_HOME": str(case / "terminal-cache"),
           "LANG": "en_US.UTF-8", "GITHUB_ACTIONS": "true", "RUNNER_ENVIRONMENT": "github-hosted"}
    scene = [sys.executable, "-I", str(root / "scene.py"), "--managed", label, mode, str(case)]
    if terminal == "kitty":
        argv = [str(executable), "--config", "NONE", "--title", case.name]
        for option in ("remember_window_size=no", "initial_window_width=1280", "initial_window_height=1100",
                       "font_family=Menlo", "font_size=13", "cursor_blink_interval=0", "enable_audio_bell=no",
                       "confirm_os_window_close=0", "update_check_interval=0", "background_opacity=1", "shell_integration=disabled"):
            argv += ["--override", option]
        argv += scene
    else:
        # Ghostty1.3.1 forwards argv to NSApplicationMain, which can interpret
        # existing positional paths after -e as extra files/tabs. Give Cocoa
        # only flags; this one owned executable directly execs our scene.
        wrapper = case / "scene-command"
        check(str(wrapper).isascii() and not any(c.isspace() for c in str(wrapper)),
              "direct command path must contain no whitespace")
        wrapper.write_text("#!/bin/sh\nexec " + shlex.join(scene) + "\n")
        wrapper.chmod(0o700)
        argv = [str(executable), "--config-default-files=false", "--title=" + case.name,
                "--font-family=Menlo", "--font-size=13", "--window-width=120", "--window-height=45",
                "--window-save-state=never", "--shell-integration=none", "--confirm-close-surface=false",
                "--quit-after-last-window-closed=true", "--cursor-style-blink=false", "--background=#000000",
                "--command=direct:" + str(wrapper)]
    report = {"terminal": terminal, "label": label, "mode": mode, "argv": argv,
              "native_appearance": "unreviewed; inspect actual decoded PNG pixels", "screenshots": {}}
    process = None
    try:
        with (case / "terminal.stdout").open("wb") as out, (case / "terminal.stderr").open("wb") as err:
            process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err,
                                       env=env, start_new_session=True)
            report["terminal_pid"] = process.pid
            for phase in (("preview",) if mode == "preview" else ("prior", "cancel", "retry")):
                ready = wait_ready(case / (phase + ".ready.json"), process, 18)
                check(ready["phase"] == phase, "incorrect checkpoint")
                # A file marker alone is not GPU completion proof. Capture the
                # visible checkpoint after settlement and require two equal
                # frames. Human pixel inspection must still see the marker.
                time.sleep(0.5)
                window = json.loads(run([window_tool, "window", str(process.pid)], env=env).stdout)
                previous = None
                settled = False
                for attempt in range(6):
                    capture = case / (phase + "-sample-" + str(attempt) + ".png")
                    run(["/usr/sbin/screencapture", "-x", "-o", "-l", str(window["window_id"]), "-t", "png", capture], env=env)
                    info = png_info(capture)
                    pixels = json.loads(run([window_tool, "pixels", capture], env=env).stdout)
                    info.update(pixels)
                    if pixels["pixel_sha256"] == previous:
                        settled = True
                        final = case / (phase + ".png")
                        shutil.copyfile(capture, final)
                        break
                    previous = pixels["pixel_sha256"]
                    time.sleep(0.15)
                check(settled, "native framebuffer did not settle; no passing capture")
                info.update({"phase": phase, "marker": ready["marker"], "window": window,
                             "capture_source": "owned native macOS window, screencapture",
                             "appearance": "pending pixel inspection"})
                record(case / (phase + ".captured.json"), info)
                report["screenshots"][phase] = info
            status = process.wait(timeout=8)
            check(status == 0, "terminal/scene failed")
            result = json.loads((case / "result.json").read_text())
            check("failure" not in result and result["originals_unchanged"] and result["outer_termios_unchanged"], "scene contract failed")
            report["scene_result"] = result
            report["process_checks"] = "pass; native appearance still pending inspection"
    except BaseException as error:
        report["failure"] = str(error)[:4096]
        report["error"] = failure_details(error)
        report["terminal_stdout_tail"] = log_tail(case / "terminal.stdout")
        report["terminal_stderr_tail"] = log_tail(case / "terminal.stderr")
        raise
    finally:
        try:
            if process is not None:
                report["driver_cleanup"] = terminate_owned_scene(case, process)
                check(not report["driver_cleanup"]["forced_terminal_shutdown"], "terminal needed forced shutdown")
                check(not report["driver_cleanup"]["scene_group_marker_remaining"], "scene did not confirm child cleanup")
            record(case / "capture-report.json", report)
        except BaseException as error:
            report["cleanup_failure"] = failure_details(error)
            try:
                record(case / "capture-report.json", report)
            finally:
                raise SceneCleanupError("scene cleanup incomplete; isolated runner teardown required") from error


def main():
    check(sys.platform == "darwin" and os.environ.get("GITHUB_ACTIONS") == "true"
          and os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted", "refusing local GUI execution")
    check(len(sys.argv) == 2, "usage: macos_driver.py OWNED_RUNNER_TEMP_DIR")
    root = Path(sys.argv[1]).resolve()
    runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve()
    check(root.parent == runner_temp and root.name == "native-images", "must use the owned runner temporary directory")
    evidence = root / "evidence"
    evidence.mkdir()
    report = {"candidate": os.environ["CANDIDATE_SHA"], "baseline": os.environ["BASELINE_SHA"],
              "runtimes": RUNTIMES, "native_appearance": "unreviewed", "mounts": [], "cases": [],
              "terminal_attempts": {},
              "workflow_sha": os.environ.get("GITHUB_SHA"),
              "runner": {k: os.environ.get(k) for k in ("RUNNER_ENVIRONMENT", "RUNNER_OS", "RUNNER_ARCH", "ImageOS", "ImageVersion")},
              "source_sha256": {f.name: digest(f) for f in SOURCE.iterdir() if f.is_file() and f.suffix in (".py", ".swift")}}
    mounts = []
    try:
        report["sw_vers"] = run(["sw_vers"]).stdout.decode()
        report["system"] = run(["uname", "-a"]).stdout.decode()
        # Capability data is diagnostic only. No driver, privacy or app setting
        # is changed, and unavailable diagnostics never count as native proof.
        try:
            graphics = run(["/usr/sbin/system_profiler", "SPDisplaysDataType", "-json",
                            "-detailLevel", "mini", "-timeout", "10"], timeout=15)
            report["host_graphics"] = {"stdout": bounded_output(graphics.stdout, 65536),
                                       "stderr": bounded_output(graphics.stderr)}
        except Exception as error:
            report["host_graphics"] = {"unavailable": failure_details(error)}
        for name in ("scene.py", "make_fixtures.py", "window_info.swift"):
            shutil.copyfile(SOURCE / name, root / name)
        run([sys.executable, "-I", root / "make_fixtures.py"])
        manifest = {"binaries": {}}
        for label, key in (("before", "BASELINE_SHA"), ("after", "CANDIDATE_SHA")):
            binary = root / "bin" / ("openai-" + label)
            manifest["binaries"][label] = {"path": "bin/" + binary.name, "sha256": digest(binary),
                "source_commit": os.environ[key], "frozen": True, "display_name": os.environ[key][:12], "status": "draft proof candidate"}
        record(root / "manifest.json", manifest)
        report["manifest"] = manifest
        window_tool = root / "window-info"
        run(["xcrun", "swiftc", "-O", root / "window_info.swift", "-o", window_tool], timeout=90)
        for terminal, runtime in RUNTIMES.items():
            attempt = {"status": "incomplete", "cases": []}
            report["terminal_attempts"][terminal] = attempt
            try:
                dmg = root / (terminal + ".dmg")
                download(runtime, dmg)
                mount = root / (terminal + "-mount")
                mount.mkdir()
                run(["hdiutil", "attach", "-readonly", "-nobrowse", "-mountpoint", mount, dmg], timeout=45)
                mounts.append(mount)
                report["mounts"].append(str(mount))
                bundle = mount / runtime["bundle"]
                run(["codesign", "--verify", "--deep", "--strict", bundle])
                signature = run(["codesign", "-dv", "--verbose=4", bundle]).stderr.decode()
                check("Identifier=" + runtime["id"] in signature, "bundle identifier mismatch")
                assessment = run(["spctl", "--assess", "--type", "execute", "--verbose=4", bundle])
                (evidence / (terminal + "-signature.txt")).write_text(signature + assessment.stderr.decode())
                executable = bundle / "Contents/MacOS" / runtime["executable"]
                version = run([executable, "--version"]).stdout.decode()
                check(runtime["version"] in version, "terminal version mismatch")
                report[terminal + "_version"] = version
                report[terminal + "_executable_sha256"] = digest(executable)
                for label in ("before", "after"):
                    for mode in ("preview", "ctrlc"):
                        attempt["current_case"] = [label, mode]
                        attempt["case_report"] = "runs/run-" + terminal + "/" + mode + "-" + label + "/capture-report.json"
                        one_case(root, terminal, executable, window_tool, label, mode)
                        attempt["cases"].append([label, mode])
                        report["cases"].append([terminal, label, mode])
                attempt.pop("current_case", None)
                attempt["status"] = "completed; native appearance unreviewed"
            except Exception as error:
                attempt["status"] = "failed"
                attempt["error"] = failure_details(error)
                # A failed renderer is independent of the other terminal. A
                # surviving scene group requires disposable-runner teardown.
                groups = list((root / "runs" / ("run-" + terminal)).glob("*/active-group"))
                if isinstance(error, SceneCleanupError) or groups:
                    attempt["cleanup_incomplete"] = True
                    attempt["remaining_group_markers"] = [str(path) for path in groups]
                    raise RuntimeError("scene cleanup incomplete; stopping further terminal attempts") from error
        failed = [name for name, attempt in report["terminal_attempts"].items() if attempt["status"] == "failed"]
        check(not failed, "native terminal attempts failed: " + ", ".join(failed))
        report["execution"] = "completed; no visual pass until independent PNG review"
    except BaseException as error:
        report["failure"] = str(error)[:4096]
        report["error"] = failure_details(error)
        raise
    finally:
        for mount in reversed(mounts):
            try:
                run(["hdiutil", "detach", mount], timeout=15)
            except Exception as error:
                report.setdefault("cleanup_errors", []).append(str(error)[:4096])
        for name in ("runs", "fixtures", "manifest.json"):
            path = root / name
            if path.is_dir():
                shutil.copytree(path, evidence / name)
            elif path.exists():
                shutil.copyfile(path, evidence / name)
        record(evidence / "run.json", report)
        if report.get("cleanup_errors"):
            raise RuntimeError("runtime detach failed; see run.json")


if __name__ == "__main__":
    main()
