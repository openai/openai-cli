#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_before_status="${DEMO_BEFORE_STATUS:-1}"
[[ "$demo_before_status" =~ ^[1-9][0-9]*$ ]]
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/schema-helper-demo-api}"
demo_start_api "$demo_runtime/requests.jsonl" "$demo_runtime/expected.schema.json"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK"
printf '\033[2J\033[H\033[?25l'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic loopback response; no live API request.'
sleep 0.5
printf '%s\n' '$ openai helpers schema \' \
  '    --model gpt-4.1-mini-2025-04-14 \' \
  '    --description "An invoice with line items" \' \
  '    --output invoice.schema.json'
sleep 0.4
if openai helpers schema --model gpt-4.1-mini-2025-04-14 \
  --description 'An invoice with line items' --output invoice.schema.json; then
  demo_status=0
else
  demo_status=$?
fi
printf '%s\t%s\n' "$DEMO_SCENE" "$demo_status" >> "$DEMO_STATUS_LOG"
printf '\nExit status: %s\n' "$demo_status"
test "$demo_status" -eq "$DEMO_EXPECTED_STATUS"
if [ "$DEMO_SCENE" = before ]; then
  test ! -e invoice.schema.json && test ! -L invoice.schema.json
  printf '%s\n' 'No schema file was created.'
else
  cmp -s invoice.schema.json "$DEMO_EXPECTED_ARTIFACT"
  cp invoice.schema.json "$DEMO_SAVED_ARTIFACT"
  printf '%s\n' 'Artifact bytes match the synthetic response.'
fi
sleep 4
SCENE

{
  echo 'feature: Responses schema artifact helper'
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "candidate / captured after commit: $demo_after_sha"
  echo "baseline expected exit status: $demo_before_status"
  echo 'data: fixed synthetic invoice schema; loopback Responses endpoint; no live API request'
  echo 'capture: real Bash PTY; stdin/stdout/stderr stay terminals; isolated homes and working directories'
  echo 'output: original CLI diagnostics and save receipt; saved artifact compared byte for byte'
  echo 'render: 80 columns x 24 rows; Menlo 26px; asciinema theme; maximum 20 fps'
  echo 'scope: terminal replay; no native terminal or live model acceptance claim'
  demo_capture_metadata
  shasum -a 256 "$demo_source/main.go" "$demo_source/record.sh" "$demo_source/README.md"
} > "$demo_output/metadata.txt"
if [ -n "${DEMO_SOURCE_MANIFEST:-}" ]; then
  cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
fi

demo_window_size=80x24
demo_render_options=(--renderer resvg --font-family Menlo --font-size 26 --line-height 1.3 \
  --theme asciinema --fps-cap 20 --last-frame-duration 4)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home" "$demo_runtime/$demo_scene-work"
  demo_label='BEFORE: pinned baseline'
  demo_expected_status="$demo_before_status"
  if [ "$demo_scene" = after ]; then
    demo_label='AFTER: generate, compile, and save'
    demo_expected_status=0
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home" \
    NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 "DEMO_SCENE=$demo_scene" \
    "DEMO_WORK=$demo_runtime/$demo_scene-work" "DEMO_EXPECTED_STATUS=$demo_expected_status" \
    "DEMO_STATUS_LOG=$demo_runtime/statuses.tsv" "DEMO_EXPECTED_ARTIFACT=$demo_runtime/expected.schema.json" \
    "DEMO_SAVED_ARTIFACT=$demo_output/invoice.schema.json"
done
demo_stop_api
cp "$demo_runtime/requests.jsonl" "$demo_output/requests.jsonl"
cp "$demo_runtime/statuses.tsv" "$demo_output/statuses.tsv"
cp "$demo_runtime/expected.schema.json" "$demo_output/expected.schema.json"
"$demo_python" - "$demo_output" "$demo_before_status" <<'PY' | tee "$demo_output/validation.txt"
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
def check(condition, message):
    if not condition:
        raise SystemExit(message)

statuses = (root / 'statuses.tsv').read_text().splitlines()
check(statuses == [f'before\t{sys.argv[2]}', 'after\t0'], 'unexpected command exit statuses')
requests = [json.loads(line) for line in (root / 'requests.jsonl').read_text().splitlines()]
check(requests == [{
    'method': 'POST', 'path': '/v1/responses', 'model': 'gpt-4.1-mini-2025-04-14',
    'synthetic_description_matched': True, 'store': False, 'max_output_tokens': 8192,
    'format': 'json_object',
}], 'expected exactly one verified synthetic Responses request')
check((root / 'invoice.schema.json').read_bytes() == (root / 'expected.schema.json').read_bytes(),
      'saved schema bytes differ from the synthetic response')
receipt = '\n'.join([
    'Saved: invoice.schema.json',
    'JSON schema compilation: passed (Draft 2020-12)',
    'Structured outputs compatibility: not checked',
    'Model: gpt-4.1-mini-2025-04-14',
    'Requests: 1',
])
after = (root / 'after.txt').read_text()
before = (root / 'before.txt').read_text()
check(receipt in after, 'candidate receipt differs from the documented outcome')
check('Saved: invoice.schema.json' not in before, 'baseline unexpectedly reported a saved artifact')
for scene in [before, after]:
    check('Synthetic loopback response; no live API request.' in scene, 'missing synthetic-data label')
print('PASS: command statuses, one request, exact artifact bytes, and original save receipt')
PY
demo_assemble_capture 400 before after
printf 'Recorded schema-helper terminal replays in %s\n' "$demo_output"
