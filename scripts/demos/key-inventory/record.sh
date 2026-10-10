#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record.sh --run-authorized-pty-slot BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
record_source="$(cd "$(dirname "$0")" && pwd)"
record_repo="$(cd "$record_source/../../.." && pwd)"
source "$record_repo/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$record_repo" "$1" "$2" "$3" "$4" "$5" "$record_source/server.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$record_source/scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: Key and service-account inventory'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'evidence: isolated Bash PTYs; synthetic API; terminal replay'
  echo 'limits: no live API or native graphical-terminal validation'
  demo_capture_metadata
  shasum -a 256 "$record_source/server.py" "$record_source/scene.sh" "$record_source/record.sh"
} > "$demo_output/metadata.txt"
demo_window_size=90x36
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_scene: key inventory" \
    "HOME=$demo_runtime/$demo_scene-home" OPENAI_ADMIN_KEY=synthetic-demo-admin NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
python3 - "$demo_output" <<'PY'
import json
import sys
from pathlib import Path
root = Path(sys.argv[1])
after = (root / 'after.txt').read_text()
before = (root / 'before.txt').read_text()
assert 'No results.' in before
assert 'No keys returned by the organization Admin-key endpoint.' in after
assert 'Project API key · proj_demo' in after
for text in (before, after):
    for expected in ['key_demo_complete_id', 'Owner project access: inactive', 'Last used at: (null)', 'Expires at: 1794092000']:
        assert expected in text, expected
requests = [json.loads(line) for line in (root / 'requests.jsonl').read_text().splitlines()]
assert len(requests) == 4 and all(row['method'] == 'GET' for row in requests)
(root / 'validation.txt').write_text('PASS: four synthetic GET requests; before/after scope; metadata and null preservation.\n')
PY
demo_assemble_capture 300 before after
printf 'Recorded key inventory in %s\n' "$demo_output"
