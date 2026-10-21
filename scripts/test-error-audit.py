#!/usr/bin/env python3
"""Exercise shard validation with synthetic Go JSON events."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("error_audit", Path(__file__).with_name("check-error-audit.py"))
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def event(action, name="", **fields):
    return {"Package": audit.PACKAGE, "Action": action, "Test": name, **fields}


def fixture(index=0, total=9):
    width, extra = divmod(total, 4)
    start = index * width + min(index, extra)
    end = start + width + (index < extra)
    metadata = {"mode": "shard", "total": total, "selected_count": end - start,
                "inventory_sha256": "a" * 64, "start": start, "end": end,
                "shard_index": index, "shard_count": 4, "representative_contract_count": 3}
    events = [event("start"), event("run", audit.TREE),
              event("output", audit.TREE, Output=audit.PREFIX + json.dumps(metadata) + "\n")]
    for number in range(start, end):
        name = f"{audit.TREE}/{number:05d}/command/invalid-type/limit"
        events.extend([event("run", name), event("pass", name)])
    events.append(event("pass", audit.TREE))
    for name in sorted(audit.GUARDS):
        events.extend([event("run", name), event("pass", name)])
    events.append(event("pass"))
    return events


class AuditTests(unittest.TestCase):
    def validate(self, events, index=0):
        return audit.validate(map(json.dumps, events), index, 4)

    def reject(self, events):
        with self.assertRaises(ValueError):
            self.validate(events)

    def test_balanced_shards_and_guard_subtests(self):
        for total in (4, 9, 13):
            union = []
            for index in range(4):
                events = fixture(index, total)
                name = sorted(audit.GUARDS)[0] + "/synthetic"
                events[1:1] = [event("run", name), event("pass", name)]
                selected = self.validate(events, index)
                union.extend(range(selected["start"], selected["end"]))
            self.assertEqual(union, list(range(total)))

    def test_missing_results(self):
        events = fixture()
        for position, item in enumerate(events):
            with self.subTest(position=position, action=item["Action"], name=item["Test"]):
                self.reject(events[:position] + events[position + 1:])
        self.reject([])
        self.reject([item for item in events if not audit.CASE.fullmatch(item["Test"])])
        for name in ("", audit.TREE, sorted(audit.GUARDS)[0], events[3]["Test"]):
            self.reject([item for item in events if item["Test"] != name])

    def test_failures_skips_and_duplicate_results(self):
        for action in ("fail", "build-fail", "skip"):
            for name in ("", audit.TREE, sorted(audit.GUARDS)[0], fixture()[3]["Test"]):
                with self.subTest(action=action, name=name):
                    self.reject(fixture() + [event(action, name)])
        events = fixture()
        for item in events:
            with self.subTest(duplicate=item):
                self.reject(events + [copy.deepcopy(item)])

    def test_case_identity_and_indices(self):
        original = fixture()[3]["Test"]
        for replacement in (original.replace("00000", "00001"), original.replace("00000", "00003"),
                            audit.TREE + "/malformed", "UnexpectedTest"):
            events = fixture()
            for item in events:
                if item["Test"] == original:
                    item["Test"] = replacement
            with self.subTest(replacement=replacement):
                self.reject(events)
        events = fixture()
        events[4]["Test"] += "/different-pass"
        self.reject(events)

    def test_invalid_metadata(self):
        original = json.loads(fixture()[2]["Output"].removeprefix(audit.PREFIX))
        changes = {"mode": ["representative", "exhaustive"], "total": [0, True, 8, "9"],
                   "selected_count": [0, 2, True], "start": [1], "end": [2, 4],
                   "shard_index": [1, False], "shard_count": [0, 3, True],
                   "representative_contract_count": [0], "inventory_sha256": ["", 42]}
        for key, values in changes.items():
            for value in values:
                events, metadata = fixture(), dict(original, **{key: value})
                events[2]["Output"] = audit.PREFIX + json.dumps(metadata)
                with self.subTest(key=key, value=value):
                    self.reject(events)
        for key in original:
            events, metadata = fixture(), dict(original)
            del metadata[key]
            events[2]["Output"] = audit.PREFIX + json.dumps(metadata)
            self.reject(events)

    def test_malformed_json_and_package(self):
        for line in ("not json", "null", "[]", '{"Action":"pass","Action":"run"}'):
            with self.subTest(line=line), self.assertRaises(ValueError):
                audit.validate([line], 0, 4)
        for key, value in (("Package", "wrong"), ("Action", 7), ("Test", None), ("Output", None)):
            events = fixture()
            events[2][key] = value
            self.reject(events)


if __name__ == "__main__":
    unittest.main()
