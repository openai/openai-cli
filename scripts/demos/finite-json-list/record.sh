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
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" /bin/bash
# Keep the wrapper under shared cleanup. exec preserves the server PID.
demo_api_binary="$demo_runtime/api.sh"
printf '#!/bin/bash\nexec %q -I -B %q "$@"\n' "$demo_python" "$demo_source/server.py" > "$demo_api_binary"
chmod 700 "$demo_api_binary"
cp "$demo_source/scene.sh" "$demo_runtime/scene.sh"
ln -s "$demo_python" "$demo_runtime/before/python3"
ln -s "$demo_python" "$demo_runtime/after/python3"
demo_start_api "$demo_output/requests.jsonl"
{
  echo 'feature: finite JSON list documents'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo 'fixtures: two synthetic files across two pages; batch purpose returns an empty list'
  echo 'capture: real CLI files; isolated Bash PTYs, environments, and homes; fake key; loopback only'
  echo 'parser: Python standard json parser; baseline parse failures are expected and recorded'
  echo 'render: terminal replay; Menlo 18px; asciinema theme; 110x46; 20fps'
  echo 'scope: no native graphical terminal, Windows, live API, installation, or publication claim'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.sh" "$demo_source/server.py" \
    "$demo_source/check.py" "$demo_python" "$demo_ffmpeg" "$demo_ffprobe"
} > "$demo_output/metadata.txt"
demo_window_size=110x46
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)
for demo_scene in before after; do
  mkdir -p "$demo_runtime/$demo_scene-home" "$demo_output/$demo_scene"
  cp "$demo_source/check.py" "$demo_output/$demo_scene/check.py"
  demo_label='BEFORE | separate JSON values | terminal replay'
  if [ "$demo_scene" = after ]; then demo_label='AFTER | one JSON array | terminal replay'; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "HOME=$demo_runtime/$demo_scene-home" "XDG_CONFIG_HOME=$demo_runtime/$demo_scene-home/config" \
    NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    "DEMO_SCENE=$demo_scene" "DEMO_DATA_DIR=$demo_output/$demo_scene" \
    "DEMO_STATUS_LOG=$demo_output/statuses.tsv"
done
demo_stop_api
"$demo_python" -I -B "$demo_source/check.py" capture "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after
printf 'Recorded finite JSON list terminal replays in %s\n' "$demo_output"
