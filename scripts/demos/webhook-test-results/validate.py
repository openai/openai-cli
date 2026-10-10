#!/usr/bin/env python3
"""Check process evidence and complete text across terminal wrapping."""

import hashlib
import json
import pathlib
import shlex
import sys


def main():
    output = pathlib.Path(sys.argv[1])
    width = int(sys.argv[2])
    requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
    assert [(request["scene"], request["operation"]) for request in requests] == [
        ("before", "test"), ("before", "test"), ("after", "test"), ("after", "test"),
        ("discovery", "catalog"), ("discovery", "catalog"), ("guided", "catalog"), ("guided", "create")]
    assert all(request["api_status"] == 200 for request in requests)
    assert len({request["response_sha256"] for request in requests[:4]}) == 1, "before/after fixtures differ"
    assert len({request["response_sha256"] for request in requests[4:7]}) == 1, "catalog fixtures differ"
    for scene in ("before", "after", "discovery", "guided"):
        assert (output / f"{scene}.status").read_text() == "0\n", "terminal exit changed"
        transcript = (output / f"{scene}.txt").read_text()
        cast = [json.loads(line) for line in (output / f"{scene}.cast").read_text().splitlines()]
        assert cast[0]["width"] == width and cast[0]["height"] == 40
        assert "Terminal replay | synthetic data" in transcript
        if scene == "guided":
            expected = ("whsec_fake_for_demo_only", "Response notifications", "https://example.com/webhook",
                        "response.completed", "response.failed", "save the signing secret", "verify signatures", "webhooks test")
        else:
            assert (output / f"{scene}-probe.status").read_text() == "0\n", "redirected exit changed"
            stdout = (output / f"{scene}-probe.stdout").read_text()
            stderr = (output / f"{scene}-probe.stderr").read_text()
            if scene == "discovery":
                fields = ("Background responses:", "Batches:", "Other:", "response.completed", "response.failed",
                          "batch.completed", "future.demo_event")
                advice = ("Repeat --event-type", "Choose events interactively: openai webhooks create")
                assert all(stdout.count(field) == 1 for field in fields), "catalog omitted or duplicated a field"
            else:
                fields = ("Test request completed.", "Delivery failed: endpoint returned HTTP 500.",
                          'Webhook endpoint ID: "wh_demo"', 'Event type: "response.completed"')
                assert stdout == "\n".join(fields) + "\n", "readable result changed"
                advice = ()
                if scene == "after":
                    advice = ("Next: check your receiver and proxy logs for this test. Fix the server error before retrying.",)
                    for prefix, command in (
                        ("Inspect the receiver URL: ", ["openai", "webhooks", "retrieve", "--webhook-endpoint-id=wh_demo"]),
                        ("After fixing the receiver, retry: ", ["openai", "webhooks", "test", "--webhook-endpoint-id=wh_demo", "--event-type=response.completed"]),
                    ):
                        lines = [line for line in stderr.splitlines() if line.startswith(prefix)]
                        assert len(lines) == 1 and shlex.split(lines[0][len(prefix):]) == command, "copied command changed"
                        advice += (lines[0],)
            assert "Next:" not in stdout, "advice polluted stdout"
            if scene == "before":
                assert not stderr, "baseline gained diagnostics"
            else:
                assert all(text in stderr for text in advice), "stderr omitted actionable advice"
            expected = fields + advice
        # A 40-column terminal wraps long sentences. Require every character after wrapping.
        compact = "".join(transcript.split())
        for text in expected:
            assert "".join(text.split()) in compact, f"missing terminal text: {text}"
    evidence = json.loads((output / "guided-evidence.json").read_text())
    assert evidence["exit_status"] == 0 and evidence["width"] == width and evidence["height"] == 24
    assert list(evidence["checkpoints"]) == ["name", "url", "events", "no-matches", "restored-events", "review", "back", "review-return", "confirm"]
    times = list(evidence["checkpoints"].values())
    assert times == sorted(times) and len(set(times)) == 9
    for name, screen in evidence["screens"].items():
        assert screen.count("Create webhook endpoint") == 1, "old form title survived"
        assert "0 selected · 4 matching events" not in screen, "old event list survived"
        if name in {"events", "restored-events", "back"}:
            assert "2 selected · 2 matching events" in screen
        elif name == "no-matches":
            assert "2 selected · 0 matching events" in screen and "No matches." in screen
        elif name in {"review", "review-return"}:
            assert "[No]   Yes, create" in screen and "Search:" not in screen
    assert "Create webhook endpoint" not in evidence["result_screen"]
    assert "Create endpoint now?" not in evidence["result_screen"]
    raw = (output / "guided-evidence.tty").read_bytes()
    assert hashlib.sha256(raw).hexdigest() == evidence["relayed_sha256"]
    child = [json.loads(line) for line in (output / "guided-evidence.child.cast").read_text().splitlines()]
    assert "".join(item[2] for item in child[1:] if item[1] == "o").encode() == raw
    outer = [json.loads(line) for line in (output / "guided.cast").read_text().splitlines()]
    capture = "".join(item[2] for item in outer[1:] if item[1] == "o")
    marker = "$ openai webhooks create\r\n"
    footer = "\r\nTerminal replay | synthetic data\r\n$ "
    assert capture.count(marker) == 1 and capture.endswith(footer)
    assert capture.split(marker, 1)[1][:-len(footer)].encode() == raw, "outer capture changed CLI bytes"
    # Match the first relayed byte to its outer event, preserving the scene labels in screenshots.
    child_start = capture.index(marker) + len(marker)
    consumed = 0
    for event in outer[1:]:
        if event[1] != "o":
            continue
        consumed += len(event[2])
        if consumed > child_start:
            offset = event[0] - child[1][0]
            break
    (output / "guided-frame-times.json").write_text(json.dumps({
        name: when + offset - 0.1 for name, when in evidence["checkpoints"].items()
    }, indent=2) + "\n")
    print("PASS: preserved result stdout and exit zero; recovery advice reaches stderr.")
    print("PASS: grouped catalog and confirmed creation retain both selected events and the synthetic signing secret.")
    print("PASS: fixture equality, eight bounded requests, terminal dimensions, and unchanged guided relay bytes.")
    print("PASS: filtered, empty-search, restored, and review screens contain one form without stale rows.")
    print("Visual inspection remains required for spacing, clipping, and timing.")


if __name__ == "__main__":
    main()
