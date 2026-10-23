#!/usr/bin/env python3
"""Capture every declared help path offline and report catalog/layout defects.

Generate the catalog with help-catalog-audit.go in a credential-free environment.
Usage: python3 scripts/audit-help.py CATALOG BINARY OUTPUT_DIRECTORY
"""
import concurrent.futures
import json
from pathlib import Path
import re
import subprocess
import sys
import tempfile

catalog_path, binary_path, output_path = map(Path, sys.argv[1:])
catalog = json.loads(catalog_path.read_text())
output_path.mkdir(parents=True, exist_ok=True)

with tempfile.TemporaryDirectory() as state:
    env = {"PATH": "/usr/bin:/bin", "HOME": state, "XDG_CONFIG_HOME": state,
           "XDG_CACHE_HOME": state, "OPENAI_CLI_NO_UPDATE_CHECK": "1",
           "OPENAI_BASE_URL": "http://127.0.0.1:1/v1", "NO_COLOR": "1",
           "LANG": "en_US.UTF-8", "TERM": "dumb"}

    def capture(item):
        index, record = item
        result = subprocess.run(["openai", *record["Path"], "--help"],
                                executable=str(binary_path.resolve()), env=env,
                                stdin=subprocess.DEVNULL, capture_output=True, timeout=20)
        text = result.stdout.decode()
        (output_path / f"{index:04}.txt").write_text(text)
        issues = []
        if result.returncode or result.stderr:
            issues.append({"kind": "process", "code": result.returncode,
                           "stderr": result.stderr.decode()})
        for line_number, line in enumerate(text.splitlines(), 1):
            if "\t" in line or line.rstrip() != line:
                issues.append({"kind": "whitespace", "line": line_number, "text": line})
            if "\x1b" in line:
                issues.append({"kind": "escape", "line": line_number})
            if len(line) > 79:
                issues.append({"kind": "width", "line": line_number, "width": len(line), "text": line})
        for flag in record["Flags"] or []:
            for name in flag["Names"]:
                prefix = "-" if len(name) == 1 else "--"
                if not re.search(re.escape(prefix + name) + r"(?![\w-])", text):
                    issues.append({"kind": "missing_flag", "name": name})
        for child in record["Children"] or []:
            if not re.search(r"^\s+" + re.escape(child) + r"(?:\s|,|$)", text, re.M):
                issues.append({"kind": "missing_child", "name": child})
        return {"index": index, "path": record["Path"], "hidden": record["Hidden"],
                "bytes": len(result.stdout), "issues": issues}

    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
        results = list(pool.map(capture, enumerate(catalog)))
    state_files = [str(p.relative_to(state)) for p in Path(state).rglob("*")]

(output_path / "audit.json").write_text(json.dumps({"results": results, "state_files": state_files}, indent=2) + "\n")
counts = {}
for item in results:
    for issue in item["issues"]:
        counts[issue["kind"]] = counts.get(issue["kind"], 0) + 1
print(json.dumps({"commands": len(results), "issue_counts": counts, "state_files": state_files}))
