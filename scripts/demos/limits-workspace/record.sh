#!/bin/bash
set -euo pipefail

if [ "$#" -ne 4 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record.sh --run-authorized-pty-slot CLI_BINARY SOURCE_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
record_source="$(cd "$(dirname "$0")" && pwd -P)"
record_root="$(cd "$record_source/../../.." && pwd -P)"
record_width="${DEMO_WIDTH:-80}"
record_no_color="${DEMO_NO_COLOR:-1}"
case "$record_width" in 80) record_height=24;; 40) record_height=12;; *) echo 'DEMO_WIDTH must be 80 or 40.' >&2; exit 2;; esac
case "$record_no_color" in 0|1) ;; *) echo 'DEMO_NO_COLOR must be 0 or 1.' >&2; exit 2;; esac
source "$record_source/../capture_and_render.sh"
# This workflow has one candidate binary. Passing it twice reuses the shared lifecycle.
# The shared cleanup also owns synthetic executable paths shown in command previews.
TMPDIR=/private/tmp demo_prepare_capture "$record_root" "$1" "$1" "$2" "$2" "$3" "$record_source/server.py"
demo_python="$(command -v python3)"
: "${DEMO_OBSERVER_SOURCES:?Set the verified observer package directory.}"
"$demo_python" -I -B "$record_source/scene.py" --check-observer > "$demo_output/observer-validation.txt"
record_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixtures.json" "$demo_runtime/gates"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' 'Terminal replay. Synthetic data.' "$DEMO_SCENE_LABEL"
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
{
  echo 'feature: compact inline limits workspace workflows'
  echo 'presentation: stable selection geometry; maximum76 columns and18rows; preserved shell scrollback'
  echo "candidate source commit: $demo_after_sha"
  echo "candidate source state: ${DEMO_SOURCE_STATE:-committed source supplied by caller}"
  echo 'baseline: no before comparison; this records the supplied workspace binary only'
  echo 'scope: synthetic loopback API; Bash PTY replay; no native graphical terminal or live API claim'
  echo "child terminal: ${record_width}x${record_height}; outer terminal: ${record_width}x$((record_height+4))"
  echo "NO_COLOR: $record_no_color; GOMAXPROCS=2; isolated HOME; CI unset"
  echo 'render: Menlo 18px, asciinema theme, shared capture_and_render lifecycle'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$record_source/record.sh" "$record_source/server.py" "$record_source/scene.py" \
  "$record_source/validate.py" "$record_source/README.md" \
  "$record_root/scripts/demos/capture_and_render.sh" "$record_root/scripts/image_picker_harness.py" \
  "$record_root/scripts/demos/audio-workbench/observer.py" "$record_root/scripts/demos/audio-workbench/observer_sources.json" \
  "$record_root/internal/app/limits_commands.go" "$record_root/internal/app/limits_service.go" \
  "$record_root/internal/app/limits_values.go" "$record_root/internal/app/limits_draft.go" \
  "$record_root/internal/app/limits_workspace.go" "$record_root/internal/app/limits_workspace_view.go" \
  "$record_root/internal/output/transform/project_limits_overview.go" > "$demo_output/source-sha256.txt"
demo_window_size="${record_width}x$((record_height+4))"
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 15 --last-frame-duration 2)
record_scenes=(org-limit org-limit-delete project-limit alert-create alert-edit alert-delete rate-update uncertain slow-cancel alert-print preflight-400 preflight-recover)
for record_scene in "${record_scenes[@]}"; do
  demo_capture_scene "$record_scene" 0 "$demo_runtime/after" "$demo_api_url/$record_scene/v1" \
    "$record_scene: ${record_width}x${record_height}" \
    "DEMO_PYTHON=$demo_python" "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_DRIVER=$record_source/scene.py" \
    "DEMO_SCENE_NAME=$record_scene" "DEMO_EVIDENCE_BASE=$demo_output/$record_scene-evidence" \
    "DEMO_WIDTH=$record_width" "DEMO_HEIGHT=$record_height" "DEMO_NO_COLOR=$record_no_color" \
    "DEMO_RUNTIME_DIRECTORY=$demo_runtime" "DEMO_OBSERVER_SOURCES=${DEMO_OBSERVER_SOURCES:?Set the verified observer package directory.}" \
    "DEMO_GATE_DIRECTORY=$demo_runtime/gates" "DEMO_REQUEST_LOG=$demo_output/requests.jsonl"
done
demo_stop_api
test "$record_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B "$record_source/validate.py" "$demo_output" > "$demo_output/validation.txt"
demo_assemble_capture 150 "${record_scenes[@]}"
# Select checkpoint frames from the exact child captures. No UI text is reconstructed.
: > "$demo_output/checkpoint-frames.txt"
for record_scene in "${record_scenes[@]}"; do
  while IFS=$'\t' read -r record_name record_seconds; do
    "$demo_agg" --quiet "${demo_render_options[@]}" --select "$record_seconds" \
      "$demo_output/$record_scene-evidence.child.cast" "$demo_output/$record_scene-$record_name-frame.gif" < /dev/null
    "$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y -i "$demo_output/$record_scene-$record_name-frame.gif" \
      -frames:v 1 "$demo_output/$record_scene-$record_name.png" < /dev/null
    printf '%s\t%s\t%s\t%s\t%s\n' "$record_scene" "$record_name" "$record_seconds" \
      "$record_scene-$record_name-frame.gif" "$record_scene-$record_name.png" >> "$demo_output/checkpoint-frames.txt"
  done < <("$demo_python" -I -B -c 'import json,sys; data=json.load(open(sys.argv[1])); [print(item["name"]+"\t"+str(item["seconds"])) for item in data["checkpoints"]]' "$demo_output/$record_scene-evidence.json")
done
"$demo_python" -I -B "$record_source/validate.py" "$demo_output" --rendered-checkpoints >> "$demo_output/validation.txt"
(
  cd "$demo_output"
  shasum -a 256 -- *.cast *.gif *.png *.txt *.json *.jsonl *.sh > SHA256SUMS
)
printf 'Recorded limits workspace terminal replay in %s\n' "$demo_output"
