#!/usr/bin/env python3
"""Require complete, successful Go JSON results for one exhaustive error-audit shard."""
import argparse
from collections import Counter, defaultdict
import json
from pathlib import Path
import re
import sys

TREE = "TestMainErrorCoverageAcrossCommandTree"
PACKAGE = "github.com/openai/openai-cli/cmd/openai"
PREFIX = "error-coverage-metadata: "
CASE = re.compile(r"^" + TREE + r"/([0-9]+)/.+$")
GUARDS = {
    "TestErrorCoverageRejectsUnclassifiedDeclarations",
    "TestErrorCoverageInventoriesInheritanceAndAliasShadowing",
    "TestErrorCoverageModelsFrameworkFlags",
    "TestErrorCoverageWorkerIsolation",
    "TestErrorCoverageRepresentativeSelection",
    "TestErrorCoverageSelectionControls",
    "TestErrorCoverageShardUnion",
}
METADATA_KEYS = {"mode", "total", "selected_count", "inventory_sha256", "start", "end",
                 "shard_index", "shard_count", "representative_contract_count"}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "Duplicate JSON field: " + key)
        result[key] = value
    return result


def validate(lines, index, count):
    metadata, tests, package = [], defaultdict(Counter), Counter()
    for line in lines:
        if not line.strip():
            continue
        event = json.loads(line, object_pairs_hook=unique_object)
        require(isinstance(event, dict), "Go JSON event must be an object")
        action, name = event.get("Action"), event.get("Test", "")
        require(isinstance(action, str) and isinstance(name, str), "Malformed Go JSON event")
        require(action not in ("fail", "build-fail", "skip"), "A test or package failed or skipped")
        require(event.get("Package") == PACKAGE, "Unexpected Go package")
        if not name:
            package[action] += 1
            continue
        require(name == TREE or CASE.fullmatch(name) or name.startswith("TestErrorCoverage"),
                "Unexpected audit test: " + name)
        tests[name][action] += 1
        output = event.get("Output", "")
        require(isinstance(output, str), "Malformed test output")
        if name == TREE and PREFIX in output:
            require(output.count(PREFIX) == 1, "Ambiguous audit metadata")
            metadata.append(json.loads(output.partition(PREFIX)[2].strip(), object_pairs_hook=unique_object))

    require(package["start"] == 1 and package["pass"] == 1, "Go package did not complete exactly once")
    require(GUARDS | {TREE} <= tests.keys(), "Missing audit parent or required guard")
    for name, actions in tests.items():
        require(actions["run"] == 1 and actions["pass"] == 1, "Test did not run and pass exactly once: " + name)
    require(len(metadata) == 1, "Audit metadata must occur exactly once")
    selected = metadata[0]
    require(isinstance(selected, dict) and selected.keys() == METADATA_KEYS, "Malformed audit metadata")
    for key in METADATA_KEYS - {"mode", "inventory_sha256"}:
        require(type(selected[key]) is int, "Audit metadata must contain integers: " + key)
    require(selected["mode"] == "shard", "CI audit requires exhaustive shard mode")
    require(type(index) is int and type(count) is int and 0 <= index < count <= selected["total"],
            "Invalid or empty shard")
    require(selected["shard_index"] == index and selected["shard_count"] == count, "Shard identity differs")
    width, extra = divmod(selected["total"], count)
    start = index * width + min(index, extra)
    end = start + width + (index < extra)
    require((selected["start"], selected["end"], selected["selected_count"]) == (start, end, end - start),
            "Shard bounds or selected count differ")
    require(selected["representative_contract_count"] > 0, "Missing representative contract inventory")
    require(isinstance(selected["inventory_sha256"], str)
            and re.fullmatch(r"[0-9a-f]{64}", selected["inventory_sha256"]), "Invalid inventory fingerprint")
    indices = sorted(int(match[1]) for name in tests if (match := CASE.fullmatch(name)))
    require(indices == list(range(start, end)), "Audit indices contain gaps, duplicates, or extras")
    return selected


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--log", type=Path, required=True)
    parser.add_argument("--shard-index", type=int, required=True)
    parser.add_argument("--shard-count", type=int, required=True)
    args = parser.parse_args()
    try:
        with args.log.open(encoding="utf-8") as source:
            result = validate(source, args.shard_index, args.shard_count)
    except (ValueError, OSError) as error:
        print("Error audit failed: " + str(error), file=sys.stderr)
        return 1
    print(f"Verified shard {args.shard_index}/{args.shard_count}: "
          f"{result['selected_count']} of {result['total']} inventory cases")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
