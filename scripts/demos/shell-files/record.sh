#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
source "$root/scripts/demos/capture_and_render.sh"
if [ "$#" -ne 6 ]; then
  echo 'usage: record.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR DEMO_API' >&2
  exit 2
fi
demo_prepare_capture "$root" "$@"
demo_window_size=100x30
demo_render_options=(--font-family Menlo --font-size 20 --theme dracula --last-frame-duration 3)
cat > "$demo_runtime/scene.sh" <<'SCENE'
set -u
cd "$DEMO_WORK"
clear
printf '%s\n\n' "$DEMO_SCENE_LABEL"
printf '$ printf '\''hello\\n'\'' | openai responses create --model MODEL --input @-\n'
printf 'hello\n' | openai responses create --model MODEL --input @-
input_status=$?
if [ "$DEMO_SCENE_SIDE" = before ]; then expected_input_status=1; else expected_input_status=0; fi
if [ "$input_status" -ne "$expected_input_status" ]; then
  printf 'Unexpected explicit-input exit status: %s\n' "$input_status" >&2
  exit 1
fi
printf '\n$ openai files content file-truncated --output previous.bin\n'
printf 'GOOD\n' > previous.bin
openai files content file-truncated --output previous.bin
status=$?
printf 'Exit status: %s\n' "$status"
if [ "$status" -ne 1 ]; then exit 1; fi
printf 'Previous file: '; cat previous.bin
printf '\n'
if [ "$DEMO_SCENE_SIDE" = before ]; then
  printf 'PARTIAL' | cmp -s - previous.bin || exit 1
else
  printf 'GOOD\n' | cmp -s - previous.bin || exit 1
fi
sleep 3
SCENE
mkdir -p "$demo_runtime/home-before" "$demo_runtime/home-after" "$demo_runtime/work-before" "$demo_runtime/work-after"
demo_start_api
demo_capture_metadata > "$demo_output/metadata.txt"
for side in before after; do
  if [ "$side" = before ]; then command_dir="$demo_runtime/before"; label='Before: pinned main'; else command_dir="$demo_runtime/after"; label='After: explicit input and reliable saves'; fi
  demo_capture_scene "$side" 0 "$command_dir" "$demo_api_url" "$label" \
    HOME="$demo_runtime/home-$side" XDG_CONFIG_HOME="$demo_runtime/home-$side" \
    DEMO_WORK="$demo_runtime/work-$side" DEMO_SCENE_SIDE="$side" FORCE_COLOR=0
 done
demo_stop_api
grep -q 'Text: hello' "$demo_output/after.txt"
grep -q 'Previous file: GOOD' "$demo_output/after.txt"
grep -q 'Previous file: PARTIAL' "$demo_output/before.txt"
grep -q 'Exit status: 1' "$demo_output/before.txt"
grep -q 'Exit status: 1' "$demo_output/after.txt"
demo_assemble_capture 300 before after
