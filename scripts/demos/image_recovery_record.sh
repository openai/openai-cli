#!/bin/bash
# Record actual binaries through the focused PTY driver and shared renderer.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: image_recovery_record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_driver="$demo_source/image_recovery_check.py"
demo_python="$(command -v python3)"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_python"
demo_window_size=104x28
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 --theme dracula --fps-cap 20 --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
demo_args=("$DEMO_BINARY" "$DEMO_CASE_OUTPUT" --scene "$DEMO_CASE")
if [ "$DEMO_BASELINE" = yes ]; then demo_args+=(--baseline); fi
exec "$DEMO_PYTHON" "$DEMO_DRIVER" "${demo_args[@]}"
SCENE
{
  echo 'feature: image generation draft recovery and explicit retry'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha (candidate working-tree binary; see candidate hash)"
  echo 'command: openai images generate'
  echo 'input: A synthetic orange robot; Enter; append with a blue hat; Enter; Ctrl+C'
  echo 'response: synthetic one-pixel PNG or first-request HTTP 400; loopback only; fake key'
  echo 'capture: native macOS zsh PTY nested inside asciinema; isolated HOME; no shell startup hooks'
  echo 'render: terminal text replay through agg; this does not validate native image graphics'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_driver" "$demo_source/image_recovery_record.sh"
} > "$demo_output/metadata.txt"
for demo_scene in before-success after-success before-retry after-retry; do
  demo_binary="$demo_after"
  demo_baseline=no
  case "$demo_scene" in before-*) demo_binary="$demo_before"; demo_baseline=yes;; esac
  demo_case="${demo_scene#*-}"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/after" 'http://127.0.0.1' "$demo_scene" \
    DEMO_PYTHON="$demo_python" DEMO_DRIVER="$demo_driver" DEMO_BINARY="$demo_binary" \
    DEMO_CASE_OUTPUT="$demo_output/$demo_scene-evidence" DEMO_CASE="$demo_case" DEMO_BASELINE="$demo_baseline"
  # Show the first result and its reopened draft before the next edit.
  demo_frame_time="$("$demo_python" - "$demo_output/$demo_scene.cast" <<'FRAME'
import json, pathlib, sys
events = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()[1:]]
text = ""
for event in events:
    if event[1] == "o":
        text += event[2]
        if any(marker in text for marker in ("Saved image:", "Request failed", "Couldn't create your image")):
            print(event[0] + 0.4)
            break
else:
    raise SystemExit("No first result in capture")
FRAME
)"
  mv "$demo_output/$demo_scene.png" "$demo_output/$demo_scene-complete.png"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_frame_time" \
    "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-draft.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-draft.gif" \
    -frames:v 1 "$demo_output/$demo_scene.png"
done
demo_assemble_capture 200 before-success after-success before-retry after-retry
printf 'Recorded image recovery replay in %s\n' "$demo_output"
