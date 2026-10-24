#!/bin/bash
set -euo pipefail
umask 077
if [ "$#" -ne 5 ]; then
  echo 'usage: record-workflows.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/workflow_fixture.py"
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/workflow_scene.sh" "$demo_runtime/scene.sh"
"$demo_python" -I -B - "$demo_source" "$demo_runtime" "$demo_output" <<'PY'
from pathlib import Path
import sys
sys.path.insert(0, sys.argv[1])
from workflow_fixture import INPUT_BYTES, write_new
runtime, output = map(Path, sys.argv[2:])
for name in ('before', 'after'):
    (runtime / (name + '-home')).mkdir(mode=0o700)
    write_new(runtime / name / 'requests.jsonl', INPUT_BYTES)
write_new(output / 'input.jsonl', INPUT_BYTES)
PY
{
  echo 'feature: Batch submission, input discovery, result matching, and readable lifecycle details'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'synthetic loopback API; fake key; isolated Bash PTYs; no model calls or charges'
  echo 'scope: terminal replay, not native graphical terminal or live API qualification'
  demo_capture_metadata
  shasum -a 256 "$demo_source/workflow_fixture.py" "$demo_source/workflow_scene.sh" "$demo_source/record-workflows.sh"
} > "$demo_output/metadata.txt"
demo_window_size=104x46
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after-submit after-results; do
  if [ "$demo_scene" = before ]; then demo_side=before; else demo_side=after; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_side" \
    "${demo_api_url%/v1}/$demo_side/v1" "$demo_scene: Batch workflows" \
    "DEMO_SCENE=$demo_scene" "DEMO_WORK=$demo_runtime/$demo_side" \
    "HOME=$demo_runtime/$demo_side-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_side-home" \
    "XDG_CACHE_HOME=$demo_runtime/$demo_side-home" OPENAI_CLI_NO_UPDATE_CHECK=1 CI=1 NO_COLOR=1 GOMAXPROCS=2
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" <<'PY'
from pathlib import Path
import json
import os
import sys
output = Path(sys.argv[1])
requests = [json.loads(line) for line in (output / 'requests.jsonl').read_text().splitlines()]
assert all(event.get('status') == 200 for event in requests), 'fixture failure'
for namespace in ('/before', '/after'):
    events = [event for event in requests if event['namespace'] == namespace]
    for path in ('/files', '/batches'):
        assert sum(event['method'] == 'POST' and event['path'] == path for event in events) == 1
before = (output / 'before.txt').read_text()
submitted = (output / 'after-submit.txt').read_text()
results = (output / 'after-results.txt').read_text()
assert 'files upload' in before and 'batches create' in before
assert 'batches submit' in submitted and 'batch_demo' in submitted
assert 'batches inputs' in results and 'requests.jsonl' in results
assert 'sentiment-positive' in results and 'positive' in results and 'matched' in results
with (output / 'validation.txt').open('x') as result:
    result.write('PASS: one upload/create per comparison; real CLI exits; input search and ID-matched result.\n')
    result.flush()
    os.fsync(result.fileno())
PY
demo_assemble_capture 300 before after-submit after-results
printf 'Recorded synthetic Batch workflows in %s\n' "$demo_output"
