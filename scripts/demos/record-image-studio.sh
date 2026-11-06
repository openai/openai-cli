#!/bin/bash
# REPO BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA EMPTY_OUTPUT
set -euo pipefail
test "$#" -eq 6
demo_root="$(cd "$1" && pwd -P)"
demo_source="$demo_root/scripts/demos"
demo_python="$(command -v python3)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$2" "$3" "$4" "$5" "$6" "$demo_python"
demo_window_size=120x32
demo_render_options=(--renderer resvg --text-font-family 'Menlo,Apple Symbols' --font-size 16 --line-height 1.2 \
  --theme dracula --fps-cap 15 --last-frame-duration 1)

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
sleep 0.7
printf '\033[2J\033[H'
demo_args=("$(command -v openai)" "$DEMO_REPORT")
if [ "$DEMO_BASELINE" = 1 ]; then demo_args+=(--baseline); fi
exec "$DEMO_PYTHON" -B -I "$DEMO_DRIVER" "${demo_args[@]}"
SCENE

{
  echo 'feature: two-pane Images studio, direct pane navigation, compact saved-image header and receipt'
  echo "baseline commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'fixture: deterministic geometric PNG; three identical synthetic response images; isolated HOME'
  echo 'capture: actual CLI bytes in 120x32 macOS PTYs; no live API, real viewer, or font registration'
  echo 'render: shared asciinema/agg lifecycle; color-block preview, not native graphical-terminal evidence'
  demo_capture_metadata
  shasum -a 256 "$demo_source/image_studio_scene.py" "$demo_source/record-image-studio.sh" \
    "$demo_source/image_workspace_scene.py" "$demo_source/image_recovery_check.py" \
    "$demo_root/scripts/image-picker-layout/drive_picker.py" "$demo_root/scripts/image_picker_harness.py"
} > "$demo_output/metadata.txt"

for demo_scene in before after; do
  demo_baseline=0
  demo_label='After: Images studio'
  if [ "$demo_scene" = before ]; then
    demo_baseline=1
    demo_label='Before: Images workspace'
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" http://127.0.0.1:1/v1 \
    "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/image_studio_scene.py" \
    "DEMO_REPORT=$demo_output/$demo_scene-checkpoints" "DEMO_BASELINE=$demo_baseline"
  mv "$demo_output/$demo_scene.png" "$demo_output/$demo_scene-exit.png"
  # Use a real saved-result checkpoint instead of the receipts printed during exit.
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/$demo_scene-checkpoints/final.cast" "$demo_output/$demo_scene-final.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -threads 1 \
    -i "$demo_output/$demo_scene-final.gif" -frames:v 1 "$demo_output/$demo_scene.png"
done
demo_assemble_capture 100 before after
printf 'Recorded review media in %s\n' "$demo_output"
