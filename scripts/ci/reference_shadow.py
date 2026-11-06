#!/usr/bin/env python3
"""Describe committed path scope only; never select, skip, or approve CI jobs."""

import argparse
import json
import os
from pathlib import Path
import re
import subprocess

SPEC = b"api_reference/openapi.transformed.yml"
METADATA = b".castiron.stats.yml"


def git(repo, *args):
    return subprocess.check_output(
        ["git", "--no-optional-locks", "-C", str(repo), *args],
        stderr=subprocess.DEVNULL, timeout=30,
    )


def classify(repo, base, head, pr_head=None):
    report = {
        "schema_version": 1, "shadow_only": True, "full_ci_required": True,
        "base": base, "head": head, "pr_head": pr_head,
        "classification": "unknown", "reason": "unavailable_history",
        "changed_files": None,
    }
    revisions = [base, head] + ([pr_head] if pr_head is not None else [])
    if not all(re.fullmatch(r"[0-9a-f]{40}", value) for value in revisions):
        report["reason"] = "invalid_revision"
        return report
    try:
        for revision in revisions:
            git(repo, "cat-file", "-e", revision + "^{commit}")
        if pr_head is not None:
            commit = git(repo, "cat-file", "commit", head).split(b"\n\n", 1)[0]
            parents = [line[7:].decode("ascii") for line in commit.splitlines()
                       if line.startswith(b"parent ")]
            if parents != [base, pr_head] or git(repo, "rev-parse", "HEAD").strip().decode() != head:
                report["reason"] = "prepared_merge_mismatch"
                return report
        flags = ["--no-renames", "--no-ext-diff", "--no-textconv"]
        raw = git(repo, "diff", "--raw", "--no-abbrev", "-z", *flags, base, head, "--")
        records = raw.split(b"\0")[:-1]
        if len(records) % 2:
            return report
        report["changed_files"] = len(records) // 2
        if not records:
            report.update(classification="no_change", reason="empty_diff")
            return report
        paths = []
        for metadata, path in zip(records[::2], records[1::2]):
            fields = metadata.split()
            if len(fields) != 5 or fields[0] != b":100644" or fields[1] != b"100644" or fields[4] != b"M":
                report.update(classification="full_ci", reason="non_regular_modification")
                return report
            if path not in (SPEC, METADATA):
                report.update(classification="full_ci", reason="outside_reference_allowlist")
                return report
            paths.append(path)
        stats = git(repo, "diff", "--numstat", "-z", *flags, base, head, "--").split(b"\0")[:-1]
        if len(stats) != len(paths) or any(
            len(row.split(b"\t", 2)) != 3 or
            not all(value.isdigit() for value in row.split(b"\t", 2)[:2])
            for row in stats
        ):
            report.update(classification="full_ci", reason="binary_or_unrecognized_diff")
            return report
        report.update(
            classification="reference_only" if SPEC in paths else "metadata_only",
            reason="text_modifications_to_allowlisted_paths",
        )
    except (subprocess.SubprocessError, OSError, UnicodeError):
        # Missing shallow parents, interrupted reads and malformed data cannot
        # produce a narrow classification. Never print Git diagnostics or paths.
        report.update(classification="unknown", reason="unavailable_history")
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, default=Path("."))
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    parser.add_argument("--pr-head")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = classify(args.repo, args.base, args.head, args.pr_head)
    args.output.write_text(json.dumps(report, indent=2) + "\n")
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as handle:
            handle.write("### Reference-only CI shadow report\n\n")
            handle.write(f"Classification: **{report['classification']}** ({report['reason']}).\n\n")
            handle.write("All CI remains required. This is path-scope evidence, not schema validation or a test verdict.\n")
    print(report["classification"])


if __name__ == "__main__":
    main()
