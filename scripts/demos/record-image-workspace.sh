#!/bin/bash
# Reuse the repository capture lifecycle. Media stays outside the product tree.
# REPO BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA EMPTY_OUTPUT
set -euo pipefail
test "$#" -eq 6
demo_root="$(cd "$1" && pwd -P)"
media_tools="$(cd "$(dirname "$0")" && pwd -P)"
demo_python="$(command -v python3)"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$2" "$3" "$4" "$5" "$6" "$demo_python"
test "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)" != "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_window_size=110x38
demo_render_options=(--renderer resvg --text-font-family 'Menlo,Apple Symbols' --font-size 18 --line-height 1.2 \
  --theme dracula \
  --fps-cap 15 --last-frame-duration 1)

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2
printf '\033[2J\033[H'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.8
printf '\033[2J\033[H'
demo_args=("$(command -v openai)" "$DEMO_REPORT" --repo "$DEMO_REPO")
if [ "$DEMO_BASELINE" = 1 ]; then demo_args+=(--baseline); fi
exec "$DEMO_PYTHON" -B -I "$DEMO_DRIVER" "${demo_args[@]}"
SCENE

{
  echo 'feature: retained Images workspace and continuous gallery'
  echo "baseline commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'fixture: deterministic geometric PNG, synthetic loopback responses, 2.5s delay'
  echo 'capture: actual CLI bytes in macOS PTYs; 110x38; synthetic settings and home'
  echo 'render: shared asciinema/agg workflow; this is not native terminal image-quality evidence'
  demo_capture_metadata
  shasum -a 256 "$media_tools/image_workspace_scene.py" "$media_tools/record-image-workspace.sh"
} > "$demo_output/metadata.txt"

for demo_scene in before after; do
  demo_baseline=0
  if [ "$demo_scene" = before ]; then demo_baseline=1; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" http://127.0.0.1:1/v1 \
    "$demo_scene: synthetic image-generation workflow" \
    "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$media_tools/image_workspace_scene.py" \
    "DEMO_REPO=$demo_root" "DEMO_REPORT=$demo_output/$demo_scene-checkpoints" \
    "DEMO_BASELINE=$demo_baseline"
  mv "$demo_output/$demo_scene.png" "$demo_output/$demo_scene-complete.png"
  # Select the real filled-form checkpoint, not the receipts displayed on exit.
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/$demo_scene-checkpoints/form.cast" "$demo_output/$demo_scene-form.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -threads 1 \
    -i "$demo_output/$demo_scene-form.gif" -vf crop=iw:380:0:0 -frames:v 1 "$demo_output/$demo_scene.png"
done
for demo_frame in results gallery-scroll retained-generating; do
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
    "$demo_output/after-checkpoints/$demo_frame.cast" "$demo_output/after-$demo_frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -threads 1 \
    -i "$demo_output/after-$demo_frame.gif" -frames:v 1 "$demo_output/after-$demo_frame.png"
done
demo_assemble_capture 100 before after
printf 'Recorded review media in %s\n' "$demo_output"
