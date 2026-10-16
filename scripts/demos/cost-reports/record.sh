#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixture.json"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: project cost reports across all Costs API pages'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'command in both scenes: openai costs report --from 2026-10-01 --to 2026-10-08 --timezone UTC --group-by project'
  echo 'before: command unavailable, exit 1, zero API requests'
  echo 'after: two API pages, proj_alpha usd 12.34 and proj_beta usd 4.56, exit 0'
  echo 'capture: isolated Bash PTYs; synthetic loopback API; fake Admin key; temporary HOME'
  echo 'settings: xterm-256color, NO_COLOR=1, FORCE_COLOR=0, GOMAXPROCS=2'
  echo 'render: 110 columns x 22 rows; Menlo 18px; asciinema theme'
  echo 'scope: terminal replay; merged stdout/stderr; no native graphical terminal or live account validation'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/server.py" \
  "$demo_root/scripts/demos/capture_and_render.sh" "$demo_root/pkg/custom/cost_report.go" \
  "$demo_root/pkg/custom/cost_report_range.go" "$demo_root/pkg/transformers/project_cost_report.go" \
  > "$demo_output/source-sha256.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi
demo_window_size=110x22
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  demo_expected_status=1
  demo_label='BEFORE: no date-range cost report'
  if [ "$demo_scene" = after ]; then
    demo_expected_status=0
    demo_label='AFTER: complete project cost report'
  fi
  demo_home="$demo_runtime/home-$demo_scene"
  mkdir -p "$demo_home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url/$demo_scene" "$demo_label" \
    "HOME=$demo_home" "XDG_CONFIG_HOME=$demo_home/config" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    OPENAI_ADMIN_KEY=synthetic-cost-report-demo-admin "DEMO_SCENE=$demo_scene" \
    "DEMO_EXPECTED_STATUS=$demo_expected_status" "DEMO_STATUS_LOG=$demo_output/statuses.tsv"
done
demo_stop_api
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B - "$demo_output" <<'PY'
import json
import pathlib
import re
import sys

output = pathlib.Path(sys.argv[1])
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert [(item["path"], item["page"], item["status"]) for item in requests] == [
    ("/after/organization/costs", "", 200),
    ("/after/organization/costs", "page-two", 200),
], requests
fixture = json.loads((output / "fixture.json").read_text())
assert all(item["response_sha256"] == fixture["pages"][item["page"]]["sha256"] for item in requests)
assert (output / "statuses.tsv").read_text().splitlines() == ["before\t1", "after\t0"]
before = (output / "before.txt").read_text()
after = (output / "after.txt").read_text()
assert "Period:" not in before and "proj_alpha" not in before
assert "costs" in before and "[exit status: 1]" in before
assert "Period: 2026-10-01 to 2026-10-08 UTC (end exclusive)" in after
assert re.search(r"(?m)^proj_alpha\s+usd\s+12\.34\s*$", after), after
assert re.search(r"(?m)^proj_beta\s+usd\s+4\.56\s*$", after), after
assert after.index("proj_alpha") < after.index("proj_beta")
assert "[exit status: 0]" in after
(output / "validation.txt").write_text(
    "PASS: identical command; baseline exit 1 with no requests; candidate exit 0 with both pages.\n"
    "PASS: exact sorted totals, period boundaries, fixture digests, and unchanged binary/source hashes.\n")
PY
demo_assemble_capture 300 before after
shasum -a 256 "$demo_output/before.png" "$demo_output/after.png" "$demo_output/comparison.gif" \
  > "$demo_output/media-sha256.txt"
printf 'Recorded project cost-report terminal replay in %s\n' "$demo_output"
