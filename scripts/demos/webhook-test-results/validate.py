#!/usr/bin/env python3
"""Check process evidence and complete text across terminal wrapping."""

import json
import pathlib
import sys


def main():
    output = pathlib.Path(sys.argv[1])
    width = int(sys.argv[2])
    requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
    assert [request["scene"] for request in requests] == ["before", "before", "after", "after", "accepted", "accepted"]
    assert all(request["api_status"] == 200 for request in requests)
    assert [request["receiver_status"] for request in requests] == [500, 500, 500, 500, 200, 200]
    assert len({request["response_sha256"] for request in requests[:4]}) == 1, "before/after fixtures differ"
    for scene in ("before", "after", "accepted"):
        assert (output / f"{scene}.status").read_text() == "0\n", "terminal exit changed"
        assert (output / f"{scene}-probe.status").read_text() == "0\n", "redirected exit changed"
        assert not (output / f"{scene}-probe.stderr").read_bytes(), "unexpected diagnostic output"
        stdout = (output / f"{scene}-probe.stdout").read_text()
        transcript = (output / f"{scene}.txt").read_text()
        cast = [json.loads(line) for line in (output / f"{scene}.cast").read_text().splitlines()]
        assert cast[0]["width"] == width and cast[0]["height"] == 24
        assert "Terminal replay | synthetic data" in transcript
        if scene == "before":
            expected = ("Success: true", "Status code: 500", "wh_demo", "response.completed")
            assert "Test request completed." not in stdout
        else:
            result, status = ("accepted", 200) if scene == "accepted" else ("failed", 500)
            expected = ("Test request completed.", f"Delivery {result}: endpoint returned HTTP {status}.",
                        'Webhook endpoint ID: "wh_demo"', 'Event type: "response.completed"')
            assert stdout == "\n".join(expected) + "\n", "readable output differs"
        # A 40-column terminal wraps long sentences. Require every character after wrapping.
        compact = "".join(transcript.split())
        for text in expected:
            assert text in stdout, f"missing redirected text: {text}"
            assert "".join(text.split()) in compact, f"missing terminal text: {text}"
    print("PASS: API success and receiver status remain distinct; every process exits zero.")
    print("PASS: redirected stdout and stderr, complete terminal text, fixture equality, and dimensions.")
    print("Visual inspection remains required for spacing, clipping, and timing.")


if __name__ == "__main__":
    main()
