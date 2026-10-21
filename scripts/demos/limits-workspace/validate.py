#!/usr/bin/env python3
"""Check recorded bytes, actual child statuses, and synthetic mutation bodies."""

import hashlib
import importlib.util
import json
import math
import re
import struct
from pathlib import Path
import sys
import termios
from urllib.parse import urlsplit


SCENES = ("org-limit", "org-limit-delete", "project-limit", "alert-create", "alert-edit",
          "alert-delete", "rate-update", "uncertain", "slow-cancel", "alert-print", "preflight-400", "preflight-recover")


def validate_child_timeline(evidence, child):
    """Bind checkpoints and the byte-free final event to actual observation time."""
    end = evidence["observation_end_seconds"]
    valid_time = lambda value: type(value) in {int, float} and math.isfinite(value) and value >= 0
    assert valid_time(end), "invalid observation end"
    assert len(child) >= 2 and child[0].get("version") == 2, "missing child cast header"
    assert (child[0].get("width"), child[0].get("height")) == (evidence["width"], evidence["height"])
    assert child[-1] == [end, "o", ""], "missing byte-free observation-end event"
    previous = 0
    for index, event in enumerate(child[1:], 1):
        assert type(event) is list and len(event) == 3, "invalid child event"
        seconds, kind, data = event
        assert valid_time(seconds) and previous <= seconds <= end, "invalid child event time"
        assert kind in {"o", "r"} and type(data) is str, "invalid child event payload"
        assert data or index == len(child) - 1 and kind == "o", "unexpected empty child event"
        previous = seconds
    previous = 0
    for point in evidence["checkpoints"]:
        seconds = point["seconds"]
        assert valid_time(seconds) and previous <= seconds <= end, "checkpoint exceeds its observation interval"
        previous = seconds


def restored_offset(raw, loading=False):
    """Require readiness and every emitted terminal reset before ordinary output."""
    ready = raw.find(b"Loading" if loading else b"Website controls and source errors: ?")
    assert ready >= 0, "workspace never reached its expected ready state"
    states, seen = {}, set()
    for match in re.finditer(rb"\x1b\[\?([0-9;]+)([hl])", raw):
        enabled = match[2] == b"h"
        for value in match[1].split(b";"):
            if not value:
                continue
            mode = int(value)
            assert mode not in (47, 1047, 1049), "workspace used an alternate screen"
            if mode in (25, 2004):
                states[mode] = enabled, match.end()
                seen.add((mode, enabled))
    for match in re.finditer(rb"\x1b\[\??([0-9;]*)J", raw):
        assert int(match[1].split(b";")[0] or b"0") not in (2, 3), "workspace cleared the screen or scrollback"
    assert (25, False) in seen and (2004, True) in seen, "workspace did not initialize expected terminal modes"
    assert states.get(25, (False, 0))[0] is True, "workspace left its cursor hidden"
    assert states.get(2004, (True, 0))[0] is False, "workspace left bracketed paste enabled"
    controls = list(re.finditer(rb"\x1b\[(?:[<>=?][ -?]*u|>[ -?]*m)", raw))
    expected = [b"\x1b[>4;2m", b"\x1b[>1u", b"\x1b[?u", b"\x1b[>4m", b"\x1b[<1u"]
    assert [item[0] for item in controls] == expected, "workspace keyboard controls differ from the pinned lifecycle"
    endings = [states[25][1], states[2004][1], controls[-2].end(), controls[-1].end()]
    assert min(endings) > ready, "terminal modes were not restored after workspace readiness"
    return max(endings)


def validate_rendered_checkpoints(root):
    validate_viewport_projections(root)
    expected_rows = []
    expected_pngs = {scene + ".png" for scene in SCENES}
    expected_frames = {scene + "-frame.gif" for scene in SCENES}
    for scene in SCENES:
        evidence = json.loads((root / (scene + "-evidence.json")).read_text())
        assert evidence["scene"] == scene
        names = set()
        for point in evidence["checkpoints"]:
            name, seconds = point["name"], point["seconds"]
            assert name and all(character in "abcdefghijklmnopqrstuvwxyz0123456789-" for character in name)
            assert name not in names, (scene, "duplicate checkpoint name", name)
            names.add(name)
            assert type(seconds) in {int, float} and math.isfinite(seconds) and seconds >= 0
            frame, png = scene + "-" + name + "-frame.gif", scene + "-" + name + ".png"
            expected_rows.append("\t".join((scene, name, str(seconds), frame, png)))
            expected_frames.add(frame)
            expected_pngs.add(png)
        assert names, (scene, "missing checkpoints")
    actual_rows = (root / "checkpoint-frames.txt").read_text().splitlines()
    assert actual_rows == expected_rows, "rendered checkpoint names, times, or filenames differ from source evidence"
    actual_pngs = {item.name for item in root.glob("*.png")}
    actual_frames = {item.name for item in root.glob("*-frame.gif")}
    assert actual_pngs == expected_pngs, ("PNG inventory mismatch", sorted(actual_pngs ^ expected_pngs))
    assert actual_frames == expected_frames, ("checkpoint GIF inventory mismatch", sorted(actual_frames ^ expected_frames))
    for name in expected_pngs | expected_frames:
        artifact = root / name
        assert artifact.is_file() and not artifact.is_symlink() and artifact.stat().st_size > 0, name
    print("PASS: rendered checkpoint names, exact times, PNGs, and frame GIFs match source evidence without missing or extra files.")


def image_size(path):
    data = path.read_bytes()
    if path.suffix == ".png":
        assert data[:8] == b"\x89PNG\r\n\x1a\n", "invalid PNG header"
        return struct.unpack(">II", data[16:24])
    assert data[:6] in (b"GIF87a", b"GIF89a"), "invalid GIF header"
    return struct.unpack("<HH", data[6:10])


def validate_viewport_projections(root):
    """Check projections against original event order, grids, cells, and cursor state."""
    spec = importlib.util.spec_from_file_location("limits_projection_driver", Path(__file__).with_name("scene.py"))
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    manifest = json.loads((root / "viewport-manifest.json").read_text())
    assert manifest["static_render_timeline"] == {"output_seconds": 0, "selection_seconds": 1, "end_seconds": 2}
    digest = lambda path: hashlib.sha256(path.read_bytes()).hexdigest()
    assert manifest["renderer_sha256"] == digest(Path(__file__).with_name("render_viewports.py"))
    assert manifest["observer_sha256"] == digest(Path(__file__).with_name("scene.py"))
    assert [entry["scene"] for entry in manifest["scenes"]] == list(SCENES)
    canvas, pixel_sizes, casts, sizes = tuple(manifest["animation_canvas"]), {}, set(), []

    def same_viewport(source, projected, padded=False):
        for row in range(source.lines):
            for column in range(source.columns):
                assert source.buffer[row][column] == projected.buffer[row][column], "projected cell or style differs"
        assert (projected.cursor.x, projected.cursor.y, projected.cursor.hidden) == (
            min(source.cursor.x, source.columns - 1), source.cursor.y, source.cursor.hidden), "projected cursor differs"
        if padded:
            for row in range(projected.lines):
                for column in range(projected.columns):
                    if row >= source.lines or column >= source.columns:
                        assert projected.buffer[row][column] == projected.default_char, "letterbox contains stale cells"

    for entry in manifest["scenes"]:
        scene = entry["scene"]
        child_path, evidence_path = root / (scene + "-evidence.child.cast"), root / (scene + "-evidence.json")
        child = [json.loads(line) for line in child_path.read_text().splitlines()]
        evidence = json.loads(evidence_path.read_text())
        sizes.append((evidence["width"], evidence["height"]))
        sizes.extend(tuple(map(int, event[2].split("x"))) for event in child[1:] if event[1] == "r")
        assert entry["source"] == {"child_cast_sha256": digest(child_path), "evidence_sha256": digest(evidence_path),
                                    "renderer_sha256": manifest["renderer_sha256"]}
        assert entry["animation"] == scene + "-viewport.cast"
        animation_path = root / entry["animation"]
        casts.add(animation_path.name)
        assert digest(animation_path) == entry["animation_sha256"]
        animation = [json.loads(line) for line in animation_path.read_text().splitlines()]
        assert (animation[0]["width"], animation[0]["height"]) == canvas
        assert animation[0]["limits_viewport"] == {**entry["source"], "projection": manifest["projection"]}
        original_events = [(index, event) for index, event in enumerate(child[1:], 1) if event[1] == "r" or event[2]]
        assert len(animation) == len(original_events) + 2
        animated_source = driver.TerminalObserver(evidence["width"], evidence["height"], lambda data: None)
        animated = driver.TerminalObserver(*canvas, lambda data: None)
        for (index, event), projected, provenance in zip(original_events, animation[1:-1], entry["animation_events"], strict=True):
            if event[1] == "r":
                animated_source.resize(*map(int, event[2].split("x")))
            else:
                animated_source.feed(event[2].encode())
            assert projected[:2] == [event[0], "o"], "animation event order or time changed"
            assert provenance == {"source_event_index": index, "seconds": event[0],
                                  "active_size": [animated_source.screen.columns, animated_source.screen.lines]}
            animated.feed(projected[2].encode())
            same_viewport(animated_source.screen, animated.screen, True)
        original = driver.TerminalObserver(evidence["width"], evidence["height"], lambda data: None)
        original_index = 1
        points = [*evidence["checkpoints"], {"name": "exit", "seconds": evidence["observation_end_seconds"]}]
        assert len(entry["checkpoints"]) == len(points)
        for point, projection in zip(points, entry["checkpoints"], strict=True):
            name, seconds = point["name"], point["seconds"]
            assert (projection["name"], projection["seconds"]) == (name, seconds)
            while original_index < len(child) and child[original_index][0] <= seconds:
                event = child[original_index]
                if event[1] == "r":
                    original.resize(*map(int, event[2].split("x")))
                else:
                    original.feed(event[2].encode())
                original_index += 1
            size = (original.screen.columns, original.screen.lines)
            assert tuple(projection["active_size"]) == size
            expected = root / (scene + "-evidence." + name + ".txt")
            assert original.text() == expected.read_text(), "original checkpoint changed"
            assert projection["cast"] == scene + "-" + name + "-viewport.cast"
            path = root / projection["cast"]
            casts.add(path.name)
            assert digest(path) == projection["sha256"]
            static = [json.loads(line) for line in path.read_text().splitlines()]
            assert (static[0]["width"], static[0]["height"]) == size
            assert static[0]["limits_viewport"] == projection["source"]
            assert projection["source"] == {**entry["source"], "checkpoint": name, "seconds": seconds,
                                             "active_size": list(size), "snapshot_sha256": digest(expected),
                                             "static_render_timeline": manifest["static_render_timeline"]}
            assert len(static) == 3 and static[1][:2] == [0, "o"]
            assert static[2] == [2, "o", ""]
            still = driver.TerminalObserver(*size, lambda data: None)
            still.feed(static[1][2].encode())
            same_viewport(original.screen, still.screen)
            base = scene if name == "exit" else scene + "-" + name
            pixels = image_size(root / (base + ".png"))
            assert pixels == image_size(root / (base + "-frame.gif")), "PNG and GIF dimensions differ"
            assert pixel_sizes.setdefault(size, pixels) == pixels, "same grid has inconsistent dimensions"
        assert animation[-1] == [evidence["observation_end_seconds"], "o", ""]
    assert {path.name for path in root.glob("*-viewport.cast")} == casts
    assert canvas == (max(size[0] for size in sizes), max(size[1] for size in sizes)), "animation canvas omits an observed grid"
    for first, first_pixels in pixel_sizes.items():
        for second, second_pixels in pixel_sizes.items():
            for dimension in (0, 1):
                if first[dimension] < second[dimension]:
                    assert first_pixels[dimension] < second_pixels[dimension], "resized viewport retained stale pixel dimensions"
    for scene in SCENES:
        assert image_size(root / (scene + ".gif")) == pixel_sizes[canvas], "animation canvas differs"


def observed_scene(root, scene, evidence, child):
    spec = importlib.util.spec_from_file_location("limits_replay_driver", Path(__file__).with_name("scene.py"))
    driver = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(driver)
    replies = []
    observer = driver.TerminalObserver(evidence["width"], evidence["height"], replies.append)
    points = iter(evidence["checkpoints"])
    point = next(points, None)
    for event in child[1:]:
        while point is not None and point["seconds"] < event[0]:
            expected = (root / (scene + "-evidence." + point["name"] + ".txt")).read_text()
            assert observer.text() == expected, (scene, point["name"], "viewport differs from captured bytes")
            point = next(points, None)
        if event[1] == "o":
            observer.feed(event[2].encode())
        elif event[1] == "r":
            width, height = map(int, event[2].split("x"))
            observer.resize(width, height)
        else:
            raise AssertionError("unexpected child capture event")
    while point is not None:
        expected = (root / (scene + "-evidence." + point["name"] + ".txt")).read_text()
        assert observer.text() == expected, (scene, point["name"], "final viewport differs from capture")
        point = next(points, None)
    assert not observer.pending, (scene, "unfinished terminal control")
    assert observer.text() == (root / (scene + "-evidence.exit.txt")).read_text()
    assert observer.history() == evidence["prior_scrollback"]["retained"]
    assert [reply.decode("ascii") for reply in replies] == evidence["protocol_replies"]
    return observer


def main():
    if len(sys.argv) not in {2, 3} or len(sys.argv) == 3 and sys.argv[2] != "--rendered-checkpoints":
        raise SystemExit("usage: validate.py OUTPUT_DIRECTORY [--rendered-checkpoints]")
    root = Path(sys.argv[1])
    if len(sys.argv) == 3:
        validate_rendered_checkpoints(root)
        return
    assert (root / "observer-validation.txt").read_text() == "PASS: observer split/coalesced queries; keyboard controls; cursor bounds; retained history.\n"
    requests = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
    for scene in SCENES:
        evidence = json.loads((root / (scene + "-evidence.json")).read_text())
        expected_status = 130 if scene == "slow-cancel" else 1 if scene in {"uncertain", "preflight-400"} else 0
        assert evidence["exit_status"] == expected_status, (scene, "incorrect child exit")
        assert evidence["cli_exit_status"] == evidence["exit_status"], (scene, "Bash changed the CLI exit status")
        assert evidence["parent_shell"] == "/bin/bash", (scene, "missing native Bash parent")
        modes = evidence["terminal_modes"]
        assert modes["snapshot_parent_pid"] == modes["bash_pid"], (scene, "terminal snapshot lacks its live Bash parent")
        initial, restored = modes["initial"].copy(), modes["restored"].copy()
        assert len(initial) == len(restored) == 7, (scene, "incomplete terminal mode snapshot")
        if sys.platform == "darwin":
            initial[3] &= ~termios.PENDIN
            restored[3] &= ~termios.PENDIN
        assert initial == restored, (scene, "terminal input mode was not restored")
        staged = evidence["staged_binary"]
        staged_path = Path(staged["path"])
        runtime = Path(evidence["recorder_runtime"])
        assert runtime.parent == Path("/private/tmp") and runtime.name.startswith("cli-demo.")
        assert staged_path.name == "openai" and staged_path.parent.parent == runtime
        assert staged_path.parent.name.startswith("openai-limits-demo-")
        assert staged["sha256_before"] == staged["sha256_after"] == evidence["binary_sha256"]
        assert staged["removed"] is True and not staged_path.parent.exists(), (scene, "staged CLI cleanup incomplete")
        assert (evidence["width"], evidence["height"]) in {(80, 24), (40, 12)}
        raw = (root / (scene + "-evidence.tty")).read_bytes()
        restored_offset(raw, loading=scene == "slow-cancel")
        child = [json.loads(line) for line in (root / (scene + "-evidence.child.cast")).read_text().splitlines()]
        validate_child_timeline(evidence, child)
        observer = observed_scene(root, scene, evidence, child)
        assert observer.bytes_observed == evidence["observer"]["bytes_observed"] == len(raw)
        assert evidence["observer"]["source_manifest_sha256"] == "9d048014421425452323fff126d2b460d33357aef64692d07f9e142b6b9888ad"
        newline = "\r\n" if initial[1] & termios.OPOST and initial[1] & termios.ONLCR else "\n"
        rows = evidence["height"] * 2 + 4 if scene == "project-limit" else 0
        prelude = "".join(f"Prior shell output {number:03d}" + newline for number in range(1, rows + 1))
        prelude += "F40 workspace start" + newline
        assert raw.startswith(prelude.encode()) and evidence["prelude_boundary"] == len(prelude.encode())
        assert evidence["prior_scrollback"]["seeded"] == (scene == "project-limit")
        if scene == "project-limit":
            expected = [f"Prior shell output {number:03d}" for number in range(1, 4)]
            positions = [observer.history().index(line) for line in expected]
            assert positions == sorted(positions), "prior scrollback changed order"
        resize_events = [event for event in child[1:] if event[1] == "r"]
        assert resize_events == evidence["resize_events"]
        target = "40x12" if evidence["width"] == 80 else "80x24"
        sizes = [event[2] for event in resize_events]
        assert sizes == (["39x11", target, f'{evidence["width"]}x{evidence["height"]}'] if scene == "alert-create" else [])
        child_bytes = "".join(event[2] for event in child[1:] if event[1] == "o").encode()
        outer = [json.loads(line) for line in (root / (scene + ".cast")).read_text().splitlines()]
        outer_bytes = "".join(event[2] for event in outer[1:] if event[1] == "o").encode()
        marker = ("$ " + " ".join(evidence["command"]) + "\r\n").encode()
        assert outer_bytes.count(marker) == 1, (scene, "ambiguous command boundary")
        relay = outer_bytes.split(marker, 1)[1]
        prompt = b"\r\n$ "
        assert relay.endswith(prompt), (scene, "missing restored prompt")
        assert relay[:-len(prompt)] == raw == child_bytes, (scene, "capture changed CLI bytes")
        assert hashlib.sha256(raw).hexdigest() == evidence["relayed_sha256"]
        if expected_status != 0:
            restored = restored_offset(raw, loading=scene == "slow-cancel")
            diagnostic = b" ".join(raw[restored:].lower().split())
            for action in (b"press r", b"use r to", b"refresh with r", b"press ?", b"press f1", b"use f1",
                           b"f1 for", b"f1:", b"ctrl+p", b"tab to apply"):
                assert action not in diagnostic, (scene, "final diagnostic names an action from the closed workspace")
        mutations = [entry for entry in requests if entry["scene"] == scene and entry["method"] in {"POST", "DELETE"}]
        assert len(mutations) == (0 if scene in {"slow-cancel", "alert-print", "preflight-400"} else 1), (scene, "unexpected mutation count")
        if scene == "slow-cancel":
            assert any(entry["scene"] == scene and entry["status"] == "held" for entry in requests)
            continue
        checkpoints = {point["name"]: point for point in evidence["checkpoints"]}
        assert "review-command" in checkpoints
        preview = (root / (scene + "-evidence.review-command.txt")).read_text()
        assert "Command preview (inspection only)" in preview, (scene, "missing inspection preview")
        assert "Command printing needs" not in preview and "Command preview omitted" not in preview
        final_screen = (root / (scene + "-evidence.exit.txt")).read_text()
        assert "> Apply change" not in final_screen, (scene, "stale confirmation after exit")
        if scene == "alert-print":
            scene_requests = [entry for entry in requests if entry["scene"] == scene]
            assert scene_requests and all(entry["method"] == "GET" for entry in scene_requests)
            assert "review-print" in checkpoints and "printed-command" in checkpoints
            assert "review-apply" not in checkpoints
            assert checkpoints["review-command"]["seconds"] < checkpoints["review-print"]["seconds"]
            keys = [event["key"] for event in evidence["keys"]]
            assert keys.count("Ctrl+P print and exit") == 1 and "Apply" not in keys
            endpoint = urlsplit(evidence["request_base_url"])
            assert endpoint.scheme == "http" and endpoint.hostname == "127.0.0.1" and endpoint.port
            assert evidence["request_base_url"] == f"http://127.0.0.1:{endpoint.port}/alert-print/v1"
            channel = json.dumps({"recipients": ["finance@example.test"],
                                  "subject_prefix": "Demo team's budget $HOME; keep 'quoted' text",
                                  "type": "email"}, sort_keys=True, separators=(",", ":"))
            command = (staged["path"] + " '--base-url=" + evidence["request_base_url"]
                       + "' admin organization spend-alerts create"
                       " --currency USD --interval month --threshold-amount 7550 --notification-channel '"
                       + channel.replace("'", "'\\''") + "'")
            newline = "\r\n" if initial[1] & termios.OPOST and initial[1] & termios.ONLCR else "\n"
            expected = (command + newline).encode()
            printed = (root / (scene + "-evidence.printed-command.raw.txt")).read_bytes()
            start = len(raw) - len(expected)
            restored_end = restored_offset(raw, loading=scene == "slow-cancel")
            assert restored_offset(raw, loading=scene == "slow-cancel") <= start
            assert raw.rfind(b"\x1b[?25h") > raw.rfind(b"\x1b[?25l") >= 0
            assert restored_end <= start and raw.rfind(b"\x1b[?25h") < start
            assert printed == expected == raw[start:], (scene, "printed command bytes changed")
            assert raw[restored_end:].count(expected) == 1
            assert evidence["printed_command"] == {"byte_offset": start, "byte_length": len(expected),
                                                   "sha256": hashlib.sha256(expected).hexdigest()}
            continue
        if scene == "preflight-400":
            scene_requests = [entry for entry in requests if entry["scene"] == scene]
            assert scene_requests and all(entry["method"] == "GET" for entry in scene_requests)
            limit_reads = [entry for entry in scene_requests if entry["path"].endswith("/spend_limit")]
            assert [entry["status"] for entry in limit_reads] == [200, 400], (scene, "preflight did not follow a successful overview")
            assert all(entry["status"] == 200 for entry in scene_requests if not entry["path"].endswith("/spend_limit"))
            required = ("review-command", "review-apply", "preflight", "refresh-required", "preflight-help", "preflight-draft")
            assert all(name in checkpoints for name in required)
            assert all(checkpoints[left]["seconds"] < checkpoints[right]["seconds"] for left, right in zip(required, required[1:]))
            keys = [event["key"] for event in evidence["keys"]]
            assert keys.count("Apply") == keys.count("Blocked review before refresh") == keys.count("F1 recovery") == 1
            for name, messages in (("preflight", ("HTTP 400", "125.25", "Review change")),
                                   ("refresh-required", ("Refresh with r", "125.25", "Review change")),
                                   ("preflight-help", ("HTTP 400", "No write sent.", "request fields and scope", "press r to refresh")),
                                   ("preflight-draft", ("125.25", "Review change"))):
                screen = (root / (scene + "-evidence." + name + ".txt")).read_text()
                compact = "".join(screen.split())
                assert all("".join(message.split()) in compact for message in messages), (scene, name, "missing recovery evidence")
            assert b"synthetic-private-preflight" not in raw, (scene, "private response leaked")
            restored_end = restored_offset(raw, loading=scene == "slow-cancel")
            diagnostic = raw[restored_end:]
            assert all(message in diagnostic for message in (b"HTTP 400", b"No write sent.", b"request fields and scope",
                                                            b"Reopen the limits workspace", b"review current settings",
                                                            b"help admin organization spend-limit retrieve",
                                                            b"original connection, authentication, and scope options"))
            assert b"may have succeeded" not in diagnostic
            continue
        if scene == "alert-create":
            required = ("validation", "validation-help", "draft-return", "review-back",
                        "review-top", "review-before-help", "review-help", "review-help-small", "review-help-resized",
                        "review-return", "review-resize-return", "review-resize-draft", "review-resized-top",
                        "review-resized-command", "review-apply", "saved")
            assert all(name in checkpoints for name in required)
            assert all(checkpoints[left]["seconds"] < checkpoints[right]["seconds"] for left, right in zip(required, required[1:]))
            for name, messages in (("validation", ("Enter a USD amount", "Review change")),
                                   ("validation-help", ("Draft details", "two decimal places.")),
                                   ("draft-return", ("> Review change", "Demo")),
                                   ("review-back", ("Monthly USD", "75.50")),
                                   ("review-return", ("> Back", "Read to the end"))):
                screen = (root / (scene + "-evidence." + name + ".txt")).read_text()
                assert all(message in screen for message in messages), (scene, name, "missing draft recovery evidence")
        if scene == "preflight-recover":
            required = ("review-command", "review-apply", "recover-failed", "recover-refreshed",
                        "refreshed-review-command", "refreshed-review-apply", "saved")
            assert all(name in checkpoints for name in required)
            assert all(checkpoints[left]["seconds"] < checkpoints[right]["seconds"] for left, right in zip(required, required[1:]))
            reads = [entry for entry in requests if entry["scene"] == scene and entry["path"].endswith("/spend_limit") and entry["method"] == "GET"]
            assert len(reads) >= 4 and [entry["status"] for entry in reads[:2]] == [200, 400]
            assert all(entry["status"] == 200 for entry in reads[2:])
            assert (root / (scene + "-evidence.recover-refreshed.txt")).read_text().count("125.25") >= 1
            assert [event["key"] for event in evidence["keys"]].count("Apply refreshed") == 1
        request = mutations[0]
        if scene in {"org-limit", "project-limit", "uncertain", "preflight-recover"}:
            assert request["body"] == {"currency": "USD", "interval": "month", "threshold_amount": 12525}
        elif scene in {"alert-create", "alert-edit"}:
            assert request["body"] == {"currency": "USD", "interval": "month",
                                       "threshold_amount": 7550 if scene == "alert-create" else 15025,
                                       "notification_channel": {"type": "email", "recipients": ["finance@example.test"],
                                                                "subject_prefix": "Demo" if scene == "alert-create" else None}}
        elif scene == "org-limit-delete":
            assert request["method"] == "DELETE" and request["path"].endswith("/spend_limit")
            assert request.get("body") is None
        elif scene == "alert-delete":
            assert request["method"] == "DELETE" and request["path"].endswith("/spend_alerts/alert_demo")
        elif scene == "rate-update":
            assert request["body"] == {"max_requests_per_1_minute": 250, "max_tokens_per_1_day": 9007199254740993}
        if scene == "uncertain":
            assert "uncertain-refreshed" in checkpoints
            assert [event["key"] for event in evidence["keys"]].count("Refresh uncertain outcome") == 1
            assert request["status"] == "response_lost_after_write"
        else:
            assert request["status"] == 200
        assert "review-command" in checkpoints and "review-apply" in checkpoints
        assert checkpoints["review-command"]["seconds"] < checkpoints["review-apply"]["seconds"]
        assert [event["key"] for event in evidence["keys"]].count("Apply") == 1
    print("PASS: twelve real workspace processes; exact child/raw/outer bytes; expected exits and mutation bodies.")
    print("PASS: one mutation per confirmed action; no retry after uncertain outcome; slow load canceled without mutation.")
    print("PASS: native Bash parents preserve CLI exits; staged binary hashes match; eleven inspection previews; staged files removed.")
    print("PASS: all seven terminal mode fields restored before Bash exits; only Darwin PENDIN normalized.")
    print("PASS: Ctrl+P exits without mutation; the complete command prints byte-for-byte after terminal restoration.")
    print("PASS: preflight HTTP 400 retains the draft; safe F1 recovery; zero mutations; normal exit preserves failure.")
    print("PASS: prior shell scrollback retained; child resize events reset Apply; draft survives help and resizing.")
    print("PASS: failed preflight refresh recovers before one write; uncertain writes remain locked after refresh.")
    print("PASS: paste, keyboard modes, and cursor reset before ordinary output; viewport checkpoints replay from exact bytes.")
    print("LIMIT: inspect all checkpoint images and GIF frames before claiming visual acceptance.")


if __name__ == "__main__":
    main()
