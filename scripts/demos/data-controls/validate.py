#!/usr/bin/env python3
"""Check recorded command statuses, response identity, and JSON preservation."""

import json
import pathlib
import sys


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: validate.py OUTPUT_DIR")
    output = pathlib.Path(sys.argv[1])
    fixture = json.loads((output / "fixture.json").read_text())
    requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
    statuses = [json.loads(line) for line in (output / "statuses.jsonl").read_text().splitlines()]
    scenes = [f"{mode}-{name}" for name in ("retention", "pending", "validated") for mode in ("before", "after")]
    assert [item["scene"] for item in requests] == [scene for scene in scenes for _ in range(2)]
    assert [item["scene"] for item in statuses] == [scene for scene in scenes for _ in range(2)]
    assert [item["format"] for item in statuses] == ["default", "json"] * len(scenes)
    assert all(item["status"] == 0 for item in statuses)
    for scene in scenes:
        mode, name = scene.split("-", 1)
        assert json.loads((output / f"{scene}.json").read_bytes()) == fixture[name]["response"], scene
        assert (output / f"{scene}.stderr").read_bytes() == b"", scene
        records = [item for item in requests if item["scene"] == scene]
        assert all(item["status"] == 200 and item["response_sha256"] == fixture[name]["sha256"] for item in records)
        captured = (output / f"{scene}.txt").read_text()
        assert "Synthetic loopback API" in captured and "$ openai" in captured, scene
        if name == "retention":
            assert "organization_default" in captured, scene
            if mode == "after":
                assert "Configured retention:" in captured and "inherit organization default" in captured, scene
                assert "Effective retention: not resolved by this response" in captured, scene
            else:
                assert "Type: organization_default" in captured and "Effective retention:" not in captured, scene
        else:
            assert "ID: ext_returned" in captured and "synthetic-demo-bucket" in captured, scene
            if mode == "after":
                assert f"Validation status: {name}" in captured, scene
                assert "Validation note:" in captured, scene
                if name == "pending":
                    assert "Validation is not complete" in captured, scene
                else:
                    assert "continuous storage health" in captured, scene
            else:
                assert f"Status: {name}" in captured and "Validation note:" not in captured, scene
    print("PASS: 12 commands returned zero; 12 expected synthetic requests completed.")
    print("PASS: paired responses match; explicit JSON preserves every response field.")
    print("PASS: inherited retention, pending validation, and validated inspection remain distinct.")


if __name__ == "__main__":
    main()
