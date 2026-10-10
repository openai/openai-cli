#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record-context-review.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR normal|narrow' >&2
  exit 2
fi
demo_profile="$6"
case "$demo_profile" in normal|narrow) ;; *) exit 2;; esac
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
# The existing server is a trap: every scene must finish without an API call.
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_source/context-review-scene.sh" "$demo_runtime/scene.sh"
demo_window_size=100x26
if [ "$demo_profile" = narrow ]; then demo_window_size=40x24; fi
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 2)
{
  echo 'feature: folder project inspection and recovery'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo "profile: $demo_profile; $demo_window_size; NO_COLOR; Menlo18"
  echo 'capture: terminal replay; synthetic local configuration; no API requests'
  demo_capture_metadata
  shasum -a 256 "$demo_source/context-review-scene.sh" "$demo_source/record-context-review.sh"
} > "$demo_output/metadata.txt"
for demo_scene in before after; do
  demo_home="$demo_runtime/$demo_scene-home"
  demo_work="$demo_runtime/$demo_scene-work"
  mkdir -p "$demo_home" "$demo_work"
  demo_registry="$demo_home/openai/project-links.json"
  if [ "$(uname -s)" = Darwin ]; then
    demo_registry="$demo_home/Library/Application Support/openai/project-links.json"
  fi
  demo_label='Before: saved draft'
  if [ "$demo_scene" = after ]; then demo_label='After: context and recovery'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "HOME=$demo_home" "USERPROFILE=$demo_home" "APPDATA=$demo_home" "XDG_CONFIG_HOME=$demo_home" \
    NO_COLOR=1 FORCE_COLOR=0 CI=1 GOMAXPROCS=2 PAGER=cat "DEMO_SCENE=$demo_scene" \
    "DEMO_PROFILE=$demo_profile" "DEMO_WORK_ROOT=$demo_work" "DEMO_REGISTRY=$demo_registry" \
    "DEMO_STATUS_LOG=$demo_output/statuses.tsv"
done
demo_stop_api
"$demo_python" -I -B - "$demo_output" "$demo_profile" <<'PY' | tee "$demo_output/validation.txt"
import pathlib
import sys

output = pathlib.Path(sys.argv[1])
profile = sys.argv[2]
assert (output / 'requests.jsonl').read_text() == '', 'local commands reached the API'
names = [('save', 0), ('failure', 1)]
if profile == 'normal':
    names = [('environment', 0), ('save', 0), ('header', 0), ('failure', 1)]
expected = [f'{scene}\t{name}\t{status}' for scene in ('before', 'after') for name, status in names]
assert (output / 'statuses.tsv').read_text().splitlines() == expected
before = (output / 'before.txt').read_text()
after = (output / 'after.txt').read_text()
for text in (before, after):
    assert 'Terminal replay | synthetic settings' in text
    assert 'synthetic-demo-key' not in text
assert 'Invalid settings were kept.' in before
assert 'invalid JSON or entries' in after
assert 'Inspect with openai link.' in after
assert 'This command did not change saved links.' in after
if profile == 'normal':
    assert 'Effective project: proj_work' not in before
    assert 'Effective project: proj_once' not in before
    assert 'Effective project: proj_work (OPENAI_CUSTOM_HEADERS fallback)' in after
    assert 'Effective project: proj_once (--header overrides the folder default)' in after
print('PASS: recorded expected command statuses and zero API requests.')
print('PASS: inspection and registry-recovery output match the reviewed contracts.')
PY
demo_assemble_capture 200 before after
printf 'Recorded context review in %s\n' "$demo_output"
