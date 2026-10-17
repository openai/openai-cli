#!/bin/bash
set -euo pipefail
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
source "$root/scripts/demos/capture_and_render.sh"
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
# This local completion demo needs no API fixture.
demo_prepare_capture "$root" "$@" /usr/bin/true
demo_window_size=94x24
demo_render_options=(--font-family Menlo --font-size 20 --theme dracula --last-frame-duration 3)
cp "$root/scripts/demos/completion-values/scene.sh" "$demo_runtime/scene.sh"
mkdir -p "$demo_runtime/home-before" "$demo_runtime/home-after"
demo_capture_metadata > "$demo_output/metadata.txt"
for side in before after; do
  if [ "$side" = before ]; then
    command_dir="$demo_runtime/before"
    label='Before: pinned main | Bash completion callback terminal replay'
  else
    command_dir="$demo_runtime/after"
    label='After: static flag values | Bash completion callback terminal replay'
  fi
  demo_capture_scene "$side" 0 "$command_dir" 'http://127.0.0.1:1' "$label" \
    HOME="$demo_runtime/home-$side" XDG_CONFIG_HOME="$demo_runtime/home-$side" \
    DEMO_SCENE_SIDE="$side" NO_COLOR=1
done
grep -q '(no value suggestions)' "$demo_output/before.txt"
grep -q 'json jsonl' "$demo_output/after.txt"
grep -q 'batch batch_output' "$demo_output/after.txt"
demo_assemble_capture 300 before after
