#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-command-subgroups.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size=90x24
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
demo_start_api "$demo_runtime/requests.txt"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
printf '$ openai admin --help\n'
sleep 0.4
if openai admin --help; then demo_status=0; else demo_status=$?; fi
printf '\n[exit %s]\n$ ' "$demo_status"
sleep 3
exit "$demo_status"
SCENE
{
  echo 'feature: nested API resource groups'
  echo "base commit: $demo_before_sha"
  echo "candidate base commit: $demo_after_sha (record working-tree diff separately if uncommitted)"
  echo 'command: openai admin --help'
  echo 'capture: actual binaries in isolated bash PTYs; no API key or live requests'
  echo 'scope: asciinema/agg terminal replay, not a graphical terminal inspection'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-command-subgroups.sh"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_capture_scene before 3 "$demo_runtime/before" "$demo_api_url" 'Before: admin is not a command'
demo_capture_scene after 0 "$demo_runtime/after" "$demo_api_url" 'After: browse admin commands'
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
/usr/bin/grep -Fq 'Unknown help topic' "$demo_output/before.txt"
/usr/bin/grep -Fq 'organization' "$demo_output/after.txt"
echo 'API requests: 0' >> "$demo_output/metadata.txt"
demo_assemble_capture 300 before after
