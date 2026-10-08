#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_width="${DEMO_WIDTH:-110}"
case "$demo_width" in 110|40) ;; *) echo 'DEMO_WIDTH must be 110 or 40.' >&2; exit 2;; esac
demo_scenes=(before after)
if [ "$demo_width" = 110 ]; then demo_scenes+=(loading); fi
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_python="$(command -v python3)"
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixture.json" "$demo_runtime/loading-gate"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' 'Synthetic API | 48 models'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER"
SCENE

{
  echo 'feature: exact model IDs and owners with loading feedback'
  echo "before source commit: $demo_before_sha"
  echo "after source commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'command in every scene: openai models list'
  echo 'fixture: 48 synthetic short IDs in descending order; three owners; identical response bytes in every scene'
  echo 'before: sorted IDs only; after: ID and OWNER columns, or complete labeled ID/owner pairs at 40 columns'
  echo 'comparison scenes: Space advances, b returns, q quits; p is not pressed'
  echo '110-column run adds one candidate loading scene; fixture releases only after Loading models appears'
  echo 'capture: shared lifecycle; exact child PTY byte relay; merged stdout/stderr; isolated temporary HOME'
  echo 'settings: xterm-256color, NO_COLOR=1, FORCE_COLOR=0, PAGER=cat, GOMAXPROCS=2; CI unset'
  echo "render: agg swash, Menlo 18px, Dracula, outer ${demo_width}x36; child terminal ${demo_width}x26"
  echo 'scope: terminal replay; no native terminal appearance or live API claim'
  demo_capture_metadata
  "$demo_python" --version
  "$demo_ffprobe" -version | sed -n '1p'
  shasum -a 256 "$demo_python" "$demo_ffmpeg" "$demo_ffprobe" /bin/bash
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/scene.py" "$demo_source/server.py" \
  "$demo_root/scripts/demos/capture_and_render.sh" "$demo_root/scripts/image_picker_harness.py" \
  "$demo_root/pkg/custom/models_list_viewer.go" "$demo_root/pkg/custom/list_navigation.go" \
  "$demo_root/pkg/custom/models_list_table.go" "$demo_root/pkg/transformers/project_models_list.go" \
  "$demo_root/pkg/custom/models_list_loading.go" \
  > "$demo_output/source-sha256.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
demo_window_size="${demo_width}x36"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in "${demo_scenes[@]}"; do
  demo_command_dir="$demo_runtime/after"
  if [ "$demo_scene" = before ]; then
    demo_label="Before: ID only ($demo_width columns)"
    demo_command_dir="$demo_runtime/before"
  elif [ "$demo_scene" = loading ]; then
    demo_label='After: Loading models while waiting'
  else
    demo_label="After: ID + owner ($demo_width columns)"
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" \
    "$demo_api_url/$demo_scene/v1" "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_DRIVER=$demo_source/scene.py" \
    "DEMO_SCENE_NAME=$demo_scene" "DEMO_EVIDENCE_BASE=$demo_output/$demo_scene-evidence" \
    "DEMO_WIDTH=$demo_width" "DEMO_FIXTURE_METADATA=$demo_output/fixture.json" \
    "DEMO_LOADING_GATE=$demo_runtime/loading-gate"
done
demo_stop_api
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B - "$demo_output" "$demo_width" <<'PY'
import hashlib, json, pathlib, sys
output = pathlib.Path(sys.argv[1])
width = int(sys.argv[2])
names = ["before", "after"] + (["loading"] if width == 110 else [])
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
assert [item["path"] for item in requests] == [f"/{scene}/v1/models" for scene in names], requests
fixture = json.loads((output / "fixture.json").read_text())
assert fixture["models"] == 48
assert all(item["status"] == 200 and item["response_sha256"] == fixture["sha256"] for item in requests)
scenes = {}
for scene in names:
    evidence = json.loads((output / (scene + "-evidence.json")).read_text())
    scenes[scene] = evidence
    recording = [json.loads(line) for line in (output / (scene + ".cast")).read_text().splitlines()]
    capture = "".join(item[2] for item in recording[1:] if item[1] == "o")
    marker = "$ openai models list\r\n"
    assert capture.count(marker) == 1, "ambiguous command boundary"
    tail = capture.split(marker, 1)[1]
    prompt = "\r\n$ "
    assert tail.endswith(prompt), "missing final shell prompt"
    relay = tail[:-len(prompt)].encode()
    raw = (output / (scene + "-evidence.tty")).read_bytes()
    child = [json.loads(line) for line in (output / (scene + "-evidence.child.cast")).read_text().splitlines()]
    child_output = "".join(item[2] for item in child[1:] if item[1] == "o").encode()
    assert relay == raw == child_output, "outer or child recording changed CLI bytes"
    assert hashlib.sha256(raw).hexdigest() == evidence["relayed_sha256"], "recorded relay digest differs"
    assert evidence["exit_status"] == 0
    assert [item["key"] for item in evidence["keys"]] == (["q"] if scene == "loading" else ["Space", "b", "q"])
    assert all(item["bytes_before"] < item["bytes_after"] < 40000 for item in evidence["keys"])
    assert 0 < len(evidence["initial_ids"]) < 48
    if scene == "loading":
        assert evidence["release_seconds"] - evidence["loading_seconds"] >= 1.2
        assert requests[-1]["held_seconds"] >= 1.2
        assert "Loading models" in (output / "loading-evidence.loading.txt").read_text()
if width == 110:
    assert scenes["before"]["initial_ids"] == scenes["after"]["initial_ids"]
(output / "validation.txt").write_text(
    "PASS: raw, child-cast, and outer-cast bytes match; all exits zero; one identical response per command.\n"
    "PASS: baseline exact IDs; candidate exact ID/owner pairs; no unrelated metadata.\n"
    "PASS: bounded viewers; forward/back rows, clean quit, and terminal restoration.\n" +
    ("PASS: delayed response shows Loading models before results; loaded output clears feedback.\n" if width == 110 else ""))
PY
demo_assemble_capture 200 "${demo_scenes[@]}"
for demo_scene in "${demo_scenes[@]}"; do
  demo_viewer_time="$("$demo_python" -I -B -c 'import json,sys; print(json.load(open(sys.argv[1]))["keys"][-1]["seconds"] - 0.5)' "$demo_output/$demo_scene-evidence.json")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_viewer_time" \
    "$demo_output/$demo_scene-evidence.child.cast" "$demo_output/$demo_scene-viewer-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-viewer-frame.gif" \
    -frames:v 1 "$demo_output/$demo_scene-viewer.png"
done
if [ "$demo_width" = 110 ]; then
  demo_loading_time="$("$demo_python" -I -B -c 'import json,sys; print(json.load(open(sys.argv[1]))["release_seconds"] - 0.4)' "$demo_output/loading-evidence.json")"
  "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_loading_time" \
    "$demo_output/loading-evidence.child.cast" "$demo_output/loading-pending-frame.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/loading-pending-frame.gif" \
    -frames:v 1 "$demo_output/loading-pending.png"
fi
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded model-list comparison in %s\n' "$demo_output"
