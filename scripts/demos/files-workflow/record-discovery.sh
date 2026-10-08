#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record-discovery.sh --run-authorized-pty-slot BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
record_source="$(cd "$(dirname "$0")" && pwd)"
record_repo="$(cd "$record_source/../../.." && pwd)"
source "$record_repo/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$record_repo" "$1" "$2" "$3" "$4" "$5" "${DEMO_API_BINARY:-$record_repo/dist/demos/bin/files-workflow-demo-api}"
demo_start_api "$demo_output/requests.jsonl"
cp "$record_source/discovery-scene.sh" "$demo_runtime/scene.sh"
{
  echo 'feature: Files workflow discovery'
  echo "evidence role: ${DEMO_EVIDENCE_ROLE:-unlabeled; do not publish}"
  echo "comparison base / before commit: $demo_before_sha"
  echo "candidate / after commit: $demo_after_sha"
  echo 'capture: isolated Bash PTYs; offline command help; zero API requests'
  echo 'scope: asciinema/agg replay; no graphical terminal validation'
  echo 'render: 110 columns x 54 rows; Menlo 18px; asciinema theme'
  demo_capture_metadata
  shasum -a 256 "$record_source/discovery-scene.sh" "$record_source/record-discovery.sh"
  go version -m "$demo_before"
  go version -m "$demo_after"
} > "$demo_output/metadata.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi
demo_window_size=110x54
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home"
  demo_label='BEFORE: files help'
  if [ "$demo_scene" = after ]; then demo_label='AFTER: discover upload, get, and download'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
done
demo_stop_api
test ! -s "$demo_output/requests.jsonl"
python3 - "$demo_output" <<'PY'
from pathlib import Path
import sys
root=Path(sys.argv[1]); after=(root/'after.txt').read_text()
required=['Upload, inspect, and download files.', 'upload', 'get', 'download', 'files upload "upload space.txt" --purpose user_data', 'files get file-example', 'files download file-example --output "downloaded copy.txt"']
for value in required:
 if value not in after: raise SystemExit('Missing discovery output: '+value)
if 'Existing create, retrieve, and content commands remain available.' not in ' '.join(after.split()):
 raise SystemExit('Missing compatibility guidance')
actions=after.split('ACTIONS',1)[1].split('Command help:',1)[0]
positions=[actions.index('  '+name+' ') for name in ['upload','get','download','list','delete']]
if positions != sorted(positions): raise SystemExit('Workflow commands are not in the expected order')
(root/'validation.txt').write_text('PASS: both help commands exit zero; no API requests; all three runnable examples; workflow ordering.\n')
PY
demo_assemble_capture 300 before after
printf 'Recorded Files discovery in %s\n' "$demo_output"
