#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/fixture.py"
demo_python="$(command -v python3)"
demo_start_api "$demo_output/requests.jsonl"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
printf '\033[2J\033[H\033[?25l'
printf '%s\n%s\n\n' 'Terminal replay | macOS Bash | synthetic loopback API' "$DEMO_SCENE_LABEL"
printf '$ openai responses retrieve resp_synthetic --stream=true --starting-after 10 \\\n    --transform sequence_number --raw-output\n'
if openai responses retrieve resp_synthetic --stream=true --starting-after 10 \
    --transform sequence_number --raw-output > "$DEMO_DATA/stream.out" 2> "$DEMO_DATA/stream.err"; then status=0; else status=$?; fi
cat "$DEMO_DATA/stream.out" "$DEMO_DATA/stream.err"
printf 'Exit: %s\n\n' "$status"
test "$status" -eq "$DEMO_STREAM_STATUS"
printf '%s\n' "$status" > "$DEMO_DATA/stream.status"
sleep 0.5
printf '$ openai models retrieve model_synthetic --format-error json\n'
if openai models retrieve model_synthetic --format-error json > "$DEMO_DATA/error.out" 2> "$DEMO_DATA/error.err"; then status=0; else status=$?; fi
cat "$DEMO_DATA/error.out" "$DEMO_DATA/error.err"
printf 'Exit: %s\n' "$status"
test "$status" -eq 1
sleep 2
SCENE
{
  echo "Before source: $demo_before_sha"
  echo "After source: $demo_after_sha"
  echo 'Separate stdout/stderr captures; fixed local API failures; isolated HOME; no live API.'
  echo 'The PTY replays redirected CLI output; the CLI stdout/stderr are files during execution.'
  echo 'This terminal replay does not establish native terminal appearance.'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/fixture.py"
} > "$demo_output/metadata.txt"
demo_window_size=100x24
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 --theme asciinema --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  demo_command_dir="$demo_runtime/$demo_scene"
  demo_status=1
  [ "$demo_scene" != before ] || demo_status=0
  mkdir -p "$demo_output/$demo_scene-data" "$demo_runtime/home-$demo_scene"
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_scene | same incomplete stream and empty error" \
    FORCE_COLOR=0 NO_COLOR=1 "HOME=$demo_runtime/home-$demo_scene" \
    "XDG_CONFIG_HOME=$demo_runtime/home-$demo_scene/config" "DEMO_DATA=$demo_output/$demo_scene-data" "DEMO_STREAM_STATUS=$demo_status"
done
demo_stop_api
"$demo_python" - "$demo_output" <<'PY'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
for scene in ("before", "after"):
    data = root / (scene + "-data")
    assert (data / "stream.out").read_bytes() == b"12\n"
    assert (data / "error.out").read_bytes() == b""
    error = json.loads((data / "error.err").read_text())
    if scene == "before":
        assert (data / "stream.err").read_bytes() == b""
        assert error == {}
    else:
        assert (data / "stream.err").read_text() == "the stream ended before the response completed\nOutput may be incomplete.\n"
        assert error == {"status_code": 502, "message": "HTTP 502 Bad Gateway: the server returned no usable JSON error details."}
assert len((root / "requests.jsonl").read_text().splitlines()) == 4
print("PASS: exact stdout, stderr, statuses, and four synthetic requests")
PY
demo_assemble_capture 200 before after
