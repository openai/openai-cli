"""Prepare synthetic release caches and verify real update-notice recordings."""

import json
import pathlib
import re
import sys
import time


def cache_path(runtime, name):
    state = runtime / f"{name}-state"
    cache = state / "home/Library/Caches" if sys.platform == "darwin" else state / "cache"
    return cache / "openai-cli/update-notice/state.json"


def seed(runtime, output):
    versions = [(output / f"{side}-version.txt").read_text().strip() for side in ("before", "after")]
    if versions[0] != versions[1]:
        raise ValueError("Build both binaries with the same runtime version for a help comparison.")
    match = re.fullmatch(r"openai version v?((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))", versions[1])
    if match is None:
        raise ValueError("The demo requires a stable numeric runtime version.")
    current = match[1]
    latest = f"{int(current.split('.')[0]) + 1}.0.0"
    for name in ("before", "after", "current"):
        state = runtime / f"{name}-state"
        for directory in ("home", "config", "cache", "data", "work"):
            (state / directory).mkdir(parents=True, mode=0o700)
        path = cache_path(runtime, name)
        path.parent.mkdir(parents=True, mode=0o700)
        data = {"schema": 1, "checked": int(time.time()) - 60, "notified": 0,
                "latest": current if name == "current" else latest}
        with path.open("x") as stream:
            json.dump(data, stream)
            stream.write("\n")
        path.chmod(0o600)
        (output / f"{name}-initial-state.json").write_bytes(path.read_bytes())
    (output / "synthetic-release.json").write_text(json.dumps({"current": current, "latest": latest}) + "\n")


def check(output):
    captions = {"before": "BEFORE: newer release cached", "after": "AFTER: newer release cached",
                "repeat": "AFTER: repeated run, same cache", "current": "AFTER: installed version is current"}
    releases = json.loads((output / "synthetic-release.json").read_text())
    notice = f"Update to {releases['latest']}: https://github.com/openai/openai-cli#installation"
    assert len(notice) <= 80, "update notice wraps in an 80-column terminal"
    captures = {}
    csi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")
    for name, caption in captions.items():
        records = [json.loads(line) for line in (output / f"{name}.cast").read_text().splitlines()]
        assert (records[0]["width"], records[0]["height"]) == (80, 28), "unexpected PTY dimensions"
        recorded = "".join(event[2] for event in records[1:] if event[1] == "o")
        marker, tail = "$ openai\r\n", f"\r\n# {caption}\r\n$ "
        assert recorded.count(marker) == 1 and recorded.endswith(tail), "ambiguous command boundaries"
        captures[name] = recorded.split(marker, 1)[1][:-len(tail)]
        plain = csi.sub("", captures[name]).replace("\r\n", "\n")
        assert "\x1b" not in plain, "unexpected terminal control sequence"
        assert "GLOBAL OPTIONS:\n" in plain and "Key setup: openai help setup\n" in plain, "missing full help"
        (output / f"{name}-cli.txt").write_text(plain)
        (output / f"{name}-cli.tty").write_bytes(captures[name].encode())
    # PTY captures can contain repeated CR immediately before LF. Preserve raw
    # evidence above; normalize only these newline bytes for the comparison.
    compared = {name: re.sub(r"\r+(?=\n)", "\r", captured) for name, captured in captures.items()}
    assert compared["after"].count(notice + "\r\n") == 1, "expected one plain update line"
    assert compared["after"].replace(notice + "\r\n", "") == compared["before"], "notice changed existing help content"
    assert compared["repeat"] == compared["before"], "repeated run changed help or repeated the notice"
    assert compared["current"] == compared["before"], "current version changed help or showed a notice"
    initial = {name: json.loads((output / f"{name}-initial-state.json").read_text())
               for name in ("before", "after", "current")}
    final = {name: json.loads((output / f"{name}-final-state.json").read_text()) for name in captions}
    assert final["before"] == initial["before"], "baseline modified the notice cache"
    assert final["after"]["notified"] > 0, "notice did not persist its throttle"
    assert {**final["after"], "notified": 0} == initial["after"], "fresh cache changed beyond notice timestamp"
    assert final["repeat"] == final["after"], "repeated run changed the notice cache"
    assert final["current"] == initial["current"], "same version consumed the notice interval"
    assert not (output / "requests.txt").read_bytes(), "unexpected API request"
    print("PASS: all four public commands exited zero in real 80x28 PTYs.")
    print("PASS: newer release adds one plain line, fitting 80 columns.")
    print("PASS: repeated runs and current versions show no notice.")
    print("PASS: all scenes preserve the same help content after PTY newline normalization.")
    print("PASS: fresh cache timestamps stay unchanged; the notice throttle persists.")
    print("PASS: rejecting API fixture received zero requests.")
    print("LIMIT: this terminal replay does not verify native graphical terminal appearance.")


if __name__ == "__main__":
    action = sys.argv[1]
    if action == "seed":
        seed(pathlib.Path(sys.argv[2]), pathlib.Path(sys.argv[3]))
    elif action == "snapshot":
        runtime, output = map(pathlib.Path, sys.argv[2:4])
        (output / f"{sys.argv[5]}-final-state.json").write_bytes(cache_path(runtime, sys.argv[4]).read_bytes())
    elif action == "check":
        check(pathlib.Path(sys.argv[2]))
    else:
        raise ValueError(f"unknown action: {action}")
