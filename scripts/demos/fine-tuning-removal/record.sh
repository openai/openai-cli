#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
# These help-only scenes require no API fixture process.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
demo_window_size=90x38
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
printf '$ openai fine-tuning --help\n\n'
if openai fine-tuning --help; then demo_status=0; else demo_status=$?; fi
sleep 3
exit "$demo_status"
SCENE
{
  echo 'feature: remove fine-tuning commands'
  echo "base commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'capture: isolated Bash PTYs, help only, no API fixture or live request'
  echo 'scope: asciinema/agg replay, not native graphical terminal acceptance'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_capture_scene before 0 "$demo_runtime/before" http://127.0.0.1:1 'Before: fine-tuning commands are available' \
  HOME="$demo_runtime" OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
demo_capture_scene after 3 "$demo_runtime/after" http://127.0.0.1:1 'After: fine-tuning commands are removed' \
  HOME="$demo_runtime" OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
grep -Fq 'jobs' "$demo_output/before.txt"
grep -Fq 'Unknown help topic.' "$demo_output/after.txt"
demo_assemble_capture 300 before after
