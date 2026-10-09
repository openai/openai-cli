#!/bin/bash
# Usage: image_navigation_spacing_record.sh BEFORE AFTER BASE_SHA CANDIDATE_SHA OUTPUT
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: image_navigation_spacing_record.sh BEFORE AFTER BASE_SHA CANDIDATE_SHA OUTPUT' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd -P)"
demo_root="$(cd "$demo_source/../.." && pwd -P)"
demo_python="$(command -v python3)"
source "$demo_source/capture_and_render.sh"
# The Python driver owns its request trap; the shared API metadata slot records Python.
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_python"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic input; zero API requests'
demo_args=("$(command -v openai)" "$DEMO_REPORT" --width 80 --relay --hold 0.6)
if [ "$DEMO_BASELINE" = 1 ]; then demo_args+=(--baseline); fi
exec "$DEMO_PYTHON" -B -I "$DEMO_DRIVER" "${demo_args[@]}"
SCENE

{
  echo 'feature: picker choice alignment and stable arrow navigation'
  echo "baseline commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'driver: existing image_picker_harness.Terminal through the public images generate command'
  echo 'data: synthetic prompt; loopback request trap; zero expected API requests'
  echo 'capture: actual macOS PTY output through the shared asciinema lifecycle'
  echo 'render: agg terminal replay; not native graphical terminal validation'
  demo_capture_metadata
  shasum -a 256 "$demo_source/image_navigation_spacing_check.py" \
    "$demo_source/image_navigation_spacing_record.sh" \
    "$demo_root/scripts/image-picker-layout/drive_picker.py" \
    "$demo_root/scripts/image_picker_harness.py" \
    "$demo_root/pkg/custom/image_picker.go" "$demo_root/pkg/custom/image_picker_view.go"
} > "$demo_output/metadata.txt"

demo_window_size=80x24
demo_render_options=(--renderer resvg --text-font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 15 --last-frame-duration 1)
for demo_scene in before after; do
  demo_label='Before | baseline'
  demo_baseline=1
  if [ "$demo_scene" = after ]; then
    demo_label='After | aligned choices and stable navigation'
    demo_baseline=0
  fi
  demo_capture_scene "$demo_scene" 130 "$demo_runtime/$demo_scene" \
    http://127.0.0.1:1/v1 "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/image_navigation_spacing_check.py" \
    "DEMO_REPORT=$demo_output/$demo_scene-checkpoints" "DEMO_BASELINE=$demo_baseline"
done
demo_assemble_capture 100 before after
printf 'Recorded picker navigation in %s\n' "$demo_output"
