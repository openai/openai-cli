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
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size='100x30'
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.4 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
demo_start_api "$demo_runtime/requests.txt"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
cd "$DEMO_WORKING_DIRECTORY"
unset OPENAI_API_KEY
printf '\033[2J\033[H$ openai\n'
sleep 0.3
"$DEMO_PYTHON" "$DEMO_DRIVER"
printf '\n$ '
sleep 3
SCENE
{
  echo 'feature: bare interactive help starts at the top'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'capture: actual binaries in isolated Bash/Python PTYs; 100x30; no personal configuration'
  echo 'data: no credentials; loopback fixture rejects and records unexpected requests'
  echo 'scope: macOS ARM64 terminal replay, not native graphical-terminal validation'
  demo_capture_metadata
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
for demo_scene in before after; do
  mkdir "$demo_runtime/home-$demo_scene"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" "$demo_api_url/help-only/v1" "$demo_scene" \
    HOME="$demo_runtime/home-$demo_scene" XDG_CONFIG_HOME="$demo_runtime/home-$demo_scene" \
    XDG_CACHE_HOME="$demo_runtime/home-$demo_scene" OPENAI_CLI_NO_UPDATE_CHECK=1 \
    DEMO_WORKING_DIRECTORY="$demo_runtime/home-$demo_scene" DEMO_PYTHON="$demo_python" \
    DEMO_DRIVER="$demo_source/scene.py" COLORTERM=truecolor
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
# The midpoint retains the first page before q restores the previous screen.
"$demo_agg" --quiet "${demo_render_options[@]}" --select 40% \
  "$demo_output/after.cast" "$demo_output/after-start.gif"
"$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/after-start.gif" \
  -frames:v 1 "$demo_output/after.png"
demo_assemble_capture 300 before after
