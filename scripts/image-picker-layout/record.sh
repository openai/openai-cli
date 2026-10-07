#!/bin/bash
# Usage: record.sh BASELINE_BINARY COLUMN_BINARY GRID_BINARY_OR_DASH BASE_SHA OUTPUT_DIR
# Build binaries outside Git. The third binary is a temporary layout experiment.
# Use - for the third argument to capture only the recommended layout revision.
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BASELINE_BINARY COLUMN_BINARY GRID_BINARY_OR_DASH BASE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd -P)"
demo_root="$(cd "$demo_source/../.." && pwd -P)"
demo_grid=""
if [ "$3" != - ]; then
  demo_grid="$(cd "$(dirname "$3")" && pwd -P)/$(basename "$3")"
fi
demo_python="$(command -v python3)"
demo_candidate_sha="$(git -C "$demo_root" rev-parse HEAD)"
source "$demo_source/../demos/capture_and_render.sh"
# The driver owns the loopback request trap. No shared API process is needed.
# Record the Python runtime in the shared dependency metadata slot.
demo_prepare_capture "$demo_root" "$1" "$2" "$4" "$demo_candidate_sha" "$5" "$demo_python"
demo_layouts=(before column column-no-color)
demo_comparison=(before-80 column-80)
if [ -n "$demo_grid" ]; then
  test -x "$demo_grid"
  mkdir "$demo_runtime/grid"
  ln -s "$demo_grid" "$demo_runtime/grid/openai"
  demo_layouts=(before column grid column-no-color)
  demo_comparison+=(grid-80)
fi

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic input; zero API requests'
demo_args=("$(command -v openai)" "$DEMO_REPORT" --width "$DEMO_WIDTH")
if [ "$DEMO_NO_COLOR" = 1 ]; then demo_args+=(--no-color); fi
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER" "${demo_args[@]}"
SCENE

{
  echo 'feature: image picker layout comparison'
  echo "baseline commit: $demo_before_sha"
  echo "candidate parent commit: $demo_after_sha"
  echo 'candidate identity: binary and source hashes below include uncommitted experiments'
  echo "baseline binary: $demo_before"
  echo "single-column binary: $demo_after"
  if [ -n "$demo_grid" ]; then
    echo "two-column experiment binary: $demo_grid"
  else
    echo 'scope: baseline versus recommended single-column layout revision'
  fi
  echo 'data: synthetic prompt; loopback request trap; zero expected API requests'
  echo 'driver: existing image_picker_harness.Terminal; public images generate command'
  echo 'capture: actual macOS PTY output through shared asciinema lifecycle'
  echo 'render: agg resvg, Menlo 20px with emoji fallback, Dracula; initial prompt screenshots'
  echo 'scope: terminal replay; not native graphical terminal or Linux/Windows evidence'
  echo 'the request trap never serves an image; no paid generation or live API access'
  demo_capture_metadata
  if [ -n "$demo_grid" ]; then shasum -a 256 "$demo_grid"; fi
  shasum -a 256 "$demo_source/record.sh" "$demo_source/drive_picker.py" \
    "$demo_source/prompt_frame.py" \
    "$demo_source/build_two_column.py" "$demo_source/two-column.patch" \
    "$demo_root/scripts/image_picker_harness.py" \
    "$demo_root/pkg/custom/image_picker.go" "$demo_root/pkg/custom/image_picker_view.go"
} > "$demo_output/metadata.txt"

demo_render_options=(--renderer resvg --text-font-family Menlo --font-size 20 --line-height 1.2 \
  --theme dracula --fps-cap 15 --last-frame-duration 1)
for demo_width in 80 40; do
  demo_window_size="${demo_width}x24"
  for demo_layout in "${demo_layouts[@]}"; do
    demo_command_dir="$demo_runtime/$demo_layout"
    demo_label="Baseline | ${demo_width}x24"
    demo_no_color=0
    case "$demo_layout" in
      column) demo_command_dir="$demo_runtime/after"; demo_label="A: single column | ${demo_width}x24";;
      grid) demo_label="B: two columns | ${demo_width}x24";;
      column-no-color)
        demo_command_dir="$demo_runtime/after"
        demo_label="A: NO_COLOR | ${demo_width}x24"
        demo_no_color=1;;
    esac
    demo_scene="${demo_layout}-${demo_width}"
    demo_capture_scene "$demo_scene" 130 "$demo_command_dir" http://127.0.0.1:1/v1 "$demo_label" \
      "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/drive_picker.py" \
      "DEMO_WIDTH=$demo_width" "DEMO_NO_COLOR=$demo_no_color" "DEMO_REPORT=$demo_output/$demo_scene.json"
    # Cancellation deliberately clears the picker. Select its stable initial frame.
    demo_prompt_frame="$("$demo_python" -I -B "$demo_source/prompt_frame.py" "$demo_output/$demo_scene.cast")"
    "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_prompt_frame" \
      "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-frame.gif"
    "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-frame.gif" \
      -frames:v 1 "$demo_output/$demo_scene.png"
  done
done
demo_assemble_capture 100 "${demo_comparison[@]}"
printf 'Recorded image picker layouts in %s\n' "$demo_output"
