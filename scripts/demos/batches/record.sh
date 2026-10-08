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
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "${DEMO_API_BINARY:?Build the batch demo API first}"
demo_start_api "$demo_runtime/requests.jsonl"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK"
printf '\033[2J\033[H\033[?25l'
printf '%s\n\n' "$DEMO_SCENE_LABEL | synthetic API"
if [ "$DEMO_SCENE" = before ]; then
  printf '%s\n' '$ openai batches retrieve batch_before'
  openai batches retrieve batch_before
  sleep 0.6
  printf '\n%s\n' '$ openai batches retrieve batch_before'
  openai batches retrieve batch_before
  printf '\n%s\n' '$ openai files content --file-id file_output --output results.jsonl'
  openai files content --file-id file_output --output results.jsonl
else
  printf '%s\n' '$ openai batches retrieve batch_after --wait --poll-interval 1s'
  openai batches retrieve batch_after --wait --poll-interval 1s
  printf '\n%s\n' '$ openai batches download batch_after --output results.jsonl'
  openai batches download batch_after --output results.jsonl
fi
printf '\n%s\n' 'Saved exact JSONL bytes. No live batch was submitted.'
sleep 3
SCENE
{
 echo 'feature: F13 Batches wait and download'
 echo "before commit: $demo_before_sha"
 echo "after commit: $demo_after_sha"
 echo 'environment: macOS native CLI processes, bash PTY, synthetic loopback API'
 echo 'scope: terminal replay, not a live API or full platform validation'
 demo_capture_metadata
 shasum -a 256 "$demo_source/main.go" "$demo_source/record.sh"
} > "$demo_output/metadata.txt"
demo_window_size=92x29
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
 demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_scene" \
  "DEMO_SCENE=$demo_scene" "DEMO_WORK=$demo_runtime/$demo_scene" NO_COLOR=1
 test -s "$demo_runtime/$demo_scene/results.jsonl"
done
cmp "$demo_runtime/before/results.jsonl" "$demo_runtime/after/results.jsonl"
demo_stop_api
cp "$demo_runtime/requests.jsonl" "$demo_output/requests.jsonl"
"$demo_python" - "$demo_output" <<'PY'
import json, pathlib, sys
p=pathlib.Path(sys.argv[1])
records=[json.loads(line) for line in (p/'requests.jsonl').read_text().splitlines()]
assert len(records)==7, records
assert sum(r['path']=='/v1/batches/batch_before' for r in records)==2
assert sum(r['path']=='/v1/batches/batch_after' for r in records)==3
assert sum(r['path']=='/v1/files/file_output/content' for r in records)==2
assert all(r['method']=='GET' for r in records)
after=(p/'after.txt').read_text()
assert 'Processing: 420 of 1000 requests finished.' in after
assert 'Completed: 1000 succeeded, 0 failed.' in after
assert 'Bytes:' in after
(p/'validation.txt').write_text('PASS: seven GET requests, exact file comparison, real exits, progress and receipt.\n')
PY
demo_assemble_capture 300 before after
printf 'Recorded F13 batch demos in %s\n' "$demo_output"
