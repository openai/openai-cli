#!/usr/bin/env python3
"""Verify every replay, command status, request, and unchanged JSON response."""

import hashlib
import json
from pathlib import Path
import sys


def check(condition, message):
    if not condition:
        raise SystemExit(message)


def main():
    check(len(sys.argv) == 2, "usage: validate.py CAPTURE_DIRECTORY")
    root = Path(sys.argv[1])
    fixtures = json.loads((root / "fixtures.json").read_text())
    metadata = (root / "metadata.txt").read_text()
    requests = []
    for width in (100, 40):
        for case in ("limit", "alerts", "json", "missing"):
            for phase in ("before", "after"):
                scene = f"{phase}-{case}-{width}"
                text = (root / f"{scene}.txt").read_text()
                compact = "".join(text.split())
                header = json.loads((root / f"{scene}.cast").read_text().splitlines()[0])
                check(header["width"] == width, f"{scene}: incorrect PTY width")
                check(header["height"] == (24 if width == 40 else 22), f"{scene}: incorrect PTY height")
                check("Terminalreplay.Syntheticdata." in compact, f"{scene}: missing evidence label")
                check(f"{scene} exit status: 0 (expected 0)" in metadata, f"{scene}: missing successful exit")
                path = "/v1/organization/spend_limit"
                if case == "alerts":
                    path = "/v1/organization/projects/proj_demo/spend_alerts"
                    check("alert_demo" in text and "finance@example.test" in text, f"{scene}: lost alert fields")
                elif case == "missing":
                    path = "/missing/v1/organization/spend_limit"
                if case == "json":
                    value, _ = json.JSONDecoder().raw_decode(text[text.index("{"):])
                    check(value == fixtures[path], f"{scene}: explicit JSON changed")
                    check("Spendthreshold:" not in compact, f"{scene}: projection leaked into JSON")
                elif phase == "before":
                    amount = "20000" if case == "alerts" else "10000"
                    check(f"Thresholdamount:{amount}" in compact, f"{scene}: baseline cents are missing")
                    check("Spendthreshold:" not in compact, f"{scene}: baseline already contains the projection")
                else:
                    amount = "200.00" if case == "alerts" else "100.00"
                    check(f"Spendthreshold:USD{amount}permonth" in compact, f"{scene}: missing units")
                    if case == "alerts":
                        check("Alertsnotify;theyarenotspendingcaps." in compact, f"{scene}: missing alert semantics")
                    elif case == "missing":
                        check("Enforcement:notreportedinthisresponse" in compact, f"{scene}: missing enforcement caveat")
                    else:
                        check("Status:enforcing" in compact, f"{scene}: reported enforcement changed")
                body = json.dumps(fixtures[path], separators=(",", ":")).encode()
                requests.append({"method": "GET", "path": path, "status": 200,
                                 "response_sha256": hashlib.sha256(body).hexdigest()})
    actual = [json.loads(line) for line in (root / "requests.jsonl").read_text().splitlines()]
    check(actual == requests, "Unexpected fixture requests, order, or response hashes")
    print("PASS: 16 successful scenes; 100/40-column PTYs; exact fixture requests and hashes.")
    print("PASS: baseline cents; readable USD/month; preserved JSON; alert semantics; missing enforcement.")


if __name__ == "__main__":
    main()
