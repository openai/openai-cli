#!/usr/bin/env python3
"""Check recorded bytes, actual child statuses, and synthetic mutation bodies."""

import hashlib
import json
from pathlib import Path
import sys
import termios
from urllib.parse import urlsplit


SCENES = ("org-limit", "org-limit-delete", "project-limit", "alert-create", "alert-edit",
          "alert-delete", "rate-update", "uncertain", "slow-cancel", "alert-print")


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: validate.py OUTPUT_DIRECTORY")
    root = Path(sys.argv[1])
    requests = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
    for scene in SCENES:
        evidence = json.loads((root / (scene + "-evidence.json")).read_text())
        expected_status = 130 if scene == "slow-cancel" else 1 if scene == "uncertain" else 0
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
        child = [json.loads(line) for line in (root / (scene + "-evidence.child.cast")).read_text().splitlines()]
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
        mutations = [entry for entry in requests if entry["scene"] == scene and entry["method"] in {"POST", "DELETE"}]
        assert len(mutations) == (0 if scene in {"slow-cancel", "alert-print"} else 1), (scene, "unexpected mutation count")
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
            restored_end = raw.rfind(b"\x1b[?1049l") + len(b"\x1b[?1049l")
            assert raw.rfind(b"\x1b[?1049l") > raw.rfind(b"\x1b[?1049h") >= 0
            assert raw.rfind(b"\x1b[?25h") > raw.rfind(b"\x1b[?25l") >= 0
            assert restored_end <= start and raw.rfind(b"\x1b[?25h") < start
            assert printed == expected == raw[start:], (scene, "printed command bytes changed")
            assert raw[restored_end:].count(expected) == 1
            assert evidence["printed_command"] == {"byte_offset": start, "byte_length": len(expected),
                                                   "sha256": hashlib.sha256(expected).hexdigest()}
            continue
        request = mutations[0]
        if scene in {"org-limit", "project-limit", "uncertain"}:
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
            assert request["body"] == {"max_requests_per_1_minute": 250}
        if scene == "uncertain":
            assert request["status"] == "response_lost_after_write"
        else:
            assert request["status"] == 200
        assert "review-command" in checkpoints and "review-apply" in checkpoints
        assert checkpoints["review-command"]["seconds"] < checkpoints["review-apply"]["seconds"]
        assert [event["key"] for event in evidence["keys"]].count("Apply") == 1
    print("PASS: ten real workspace processes; exact child/raw/outer bytes; expected exits and mutation bodies.")
    print("PASS: one mutation per confirmed action; no retry after uncertain outcome; slow load canceled without mutation.")
    print("PASS: native Bash parents preserve CLI exits; staged binary hashes match; nine inspection previews; staged files removed.")
    print("PASS: all seven terminal mode fields restored before Bash exits; only Darwin PENDIN normalized.")
    print("PASS: Ctrl+P exits without mutation; the complete command prints byte-for-byte after terminal restoration.")
    print("LIMIT: inspect all checkpoint images and GIF frames before claiming visual acceptance.")


if __name__ == "__main__":
    main()
