#!/usr/bin/env python3
"""Parse the captured CLI bytes and check the complete comparison."""

import json
import pathlib
import sys


def inspect(mode, path):
    try:
        text = path.read_text()
        if mode == "array":
            value = json.loads(text)
            if not isinstance(value, list):
                raise ValueError("the document is not a JSON array")
            print(f"VALID JSON array: {len(value)} items")
        else:
            value = [json.loads(line) for line in text.splitlines()]
            print(f"VALID JSONL: {len(value)} records")
        return 0
    except (ValueError, OSError) as error:
        print(f"INVALID {mode.upper()}: {error}")
        return 1


def validate(root):
    expected = [
        {"id": "file-demo-a", "object": "file", "filename": "alpha.txt", "purpose": "user_data"},
        {"id": "file-demo-b", "object": "file", "filename": "beta.txt", "purpose": "user_data"},
    ]
    expected_statuses = []
    for scene in ("before", "after"):
        folder = root / scene
        for name in ("files", "empty", "jsonl"):
            assert (folder / f"{name}.stderr").read_bytes() == b"", (scene, name, "stderr")
            expected_statuses.append(f"{scene}\t{name}-cli\t0")
            status = 1 if scene == "before" and name != "jsonl" else 0
            expected_statuses.append(f"{scene}\t{name}-parser\t{status}")
        assert [json.loads(line) for line in (folder / "files.jsonl").read_text().splitlines()] == expected
        transcript = (root / f"{scene}.txt").read_text()
        assert "terminal replay" in transcript
        assert "VALID JSONL: 2 records" in transcript
    assert (root / "statuses.tsv").read_text().splitlines() == expected_statuses
    assert json.loads((root / "after/files.json").read_text()) == expected
    assert json.loads((root / "after/empty.json").read_text()) == []
    assert (root / "before/empty.json").read_bytes() == b""
    before = (root / "before/files.json").read_text()
    try:
        json.loads(before)
    except json.JSONDecodeError as error:
        assert error.msg == "Extra data"
    else:
        raise AssertionError("baseline unexpectedly produced one JSON document")
    decoder, values = json.JSONDecoder(), []
    while before.strip():
        value, end = decoder.raw_decode(before.lstrip())
        values.append(value)
        before = before.lstrip()[end:]
    assert values == expected
    requests = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
    assert [item["scene"] for item in requests] == ["before"] * 5 + ["after"] * 5
    assert [{k: v for k, v in item.items() if k != "scene"} for item in requests[:5]] == [
        {k: v for k, v in item.items() if k != "scene"} for item in requests[5:]]
    assert [item["query"].get("after") for item in requests[:5]] == [None, ["file-demo-a"], None, None, ["file-demo-a"]]
    assert requests[2]["query"]["purpose"] == ["batch"]
    print("PASS: exact records, empty array, preserved JSONL, statuses, stderr, and identical synthetic API pages")


if __name__ == "__main__":
    if len(sys.argv) != 3 or sys.argv[1] not in {"array", "jsonl", "capture"}:
        raise SystemExit("usage: check.py array|jsonl FILE, or check.py capture OUTPUT_DIR")
    if sys.argv[1] == "capture":
        validate(pathlib.Path(sys.argv[2]))
    else:
        raise SystemExit(inspect(sys.argv[1], pathlib.Path(sys.argv[2])))
