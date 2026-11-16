#!/bin/bash
set -euo pipefail
if [[ $# -ne 5 ]]; then
  echo 'usage: record-inspection.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
here="$(cd "$(dirname "$0")" && pwd)"
source "$here/../capture_and_render.sh"
demo_prepare_capture "$here/../../.." "$1" "$2" "$3" "$4" "$5" "$here/server.py"
demo_start_api "$demo_output/requests.jsonl"
demo_window_size=52x21
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2
  --theme asciinema --fps-cap 20 --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
set -eu
test -t 0 && test -t 1
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
printf '$ openai projects list --max-items 1\n'
openai projects list --max-items 1
sleep 1
printf '\n$ openai projects retrieve proj_demo\n'
openai projects retrieve proj_demo
sleep 2
SCENE
{
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  demo_capture_metadata
} > "$demo_output/metadata.txt"
for version in before after; do
  mkdir -p "$demo_runtime/$version-home"
  demo_capture_scene "$version" 0 "$demo_runtime/$version" "$demo_api_url" "$version: project inspection" \
    "HOME=$demo_runtime/$version-home" OPENAI_ADMIN_KEY=synthetic-admin-key \
    COLORTERM=truecolor 'COLORFGBG=15;0' OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
done
demo_stop_api
grep -Fq '1787420228' "$demo_output/before.txt"
grep -Eq 'NAME +STATUS +ID' "$demo_output/after.txt"
grep -Eq 'Created: +2026-08-22 17:37:08 UTC' "$demo_output/after.txt"
grep -Fq '38;2;138;168;255mName:' "$demo_output/after.cast"
test "$(wc -l < "$demo_output/requests.jsonl")" -eq 4
diff <(head -n 2 "$demo_output/requests.jsonl") <(tail -n 2 "$demo_output/requests.jsonl")
demo_assemble_capture 200 before after
