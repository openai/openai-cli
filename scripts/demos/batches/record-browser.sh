#!/bin/bash
set -euo pipefail
umask 077
if [ "$#" -ne 5 ]; then
  echo 'usage: record-browser.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_python"
printf '#!/bin/bash\nexec %q -I -B %q "$@"\n' "$demo_python" "$demo_source/browser_capture_api.py" > "$demo_runtime/api"
chmod 700 "$demo_runtime/api"
demo_api_binary="$demo_runtime/api"
demo_start_api "$demo_output/requests.jsonl"
printf '#!/bin/bash\nexec %q -I -B %q\n' "$demo_python" "$demo_source/browser_capture_scene.py" > "$demo_runtime/scene.sh"
{
  echo 'feature: guided Batch navigation; Before uses manual IDs, After selects the same batch'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'synthetic loopback API; fake key; isolated homes; actual macOS CLI PTYs'
  echo 'scope: terminal replay; not physical terminal appearance, live API, or native Windows/Linux qualification'
  demo_capture_metadata
  shasum -a 256 "$demo_source/try_browser.py" "$demo_source/workflow_fixture.py" "$demo_source/tickets.csv" \
    "$demo_source/browser_capture_api.py" "$demo_source/browser_capture_scene.py" "$demo_source/record-browser.sh" \
    "$demo_root/scripts/image_picker_harness.py"
} > "$demo_output/metadata.txt"
demo_window_size=104x42
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "${demo_api_url%/v1}/$demo_scene/v1" "$demo_scene: Batch navigation" \
    "DEMO_SCENE=$demo_scene" "DEMO_EVIDENCE=$demo_output/$demo_scene-evidence" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home" \
    "XDG_CACHE_HOME=$demo_runtime/$demo_scene-home" OPENAI_CLI_NO_UPDATE_CHECK=1 "NO_COLOR=${NO_COLOR:-}" GOMAXPROCS=2
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" <<'PY'
import json
from pathlib import Path
import sys
output = Path(sys.argv[1])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
assert requests and all(row['method'] == 'GET' and row['status'] == 200 for row in requests)
for namespace in ('/before', '/after'):
    paths = [row['path'] for row in requests if row['namespace'] == namespace]
    assert '/batches' in paths and '/files/file_partial/content' in paths and '/files/file_error/content' in paths
for scene in ('before', 'after'):
    evidence = json.loads((output / (scene+'-evidence.json')).read_text())
    assert all(command['exit'] == 0 for command in evidence['commands'])
(output/'validation.txt').write_text('PASS: actual CLI exits, matching synthetic batch, readable answers/errors, GET-only requests, and joined children.\n')
PY
demo_assemble_capture 200 before after
for demo_checkpoint in menu results errors; do
  demo_event="$($demo_python -I -B -c 'import json,sys;point=json.load(open(sys.argv[1]))["checkpoints"][sys.argv[2]];events=[json.loads(line) for line in open(sys.argv[3])][1:];print(max(i for i,event in enumerate(events) if event[0]<=point))' "$demo_output/after-evidence.json" "$demo_checkpoint" "$demo_output/after-evidence.child.cast")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "event:$demo_event" "$demo_output/after-evidence.child.cast" "$demo_output/after-$demo_checkpoint.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/after-$demo_checkpoint.gif" -frames:v 1 "$demo_output/after-$demo_checkpoint.png"
done
printf 'Recorded synthetic Batch navigation in %s\n' "$demo_output"
