#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY BEFORE_COMMIT AFTER_BINARY AFTER_COMMIT EMPTY_OUTPUT_DIRECTORY' >&2
  exit 2
fi
demo_recipe="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_recipe/../../.." && pwd)"
demo_inline="$1"
demo_before_commit="$2"
demo_candidate="$3"
demo_after_commit="$4"
demo_destination="$5"
demo_fixture="$demo_recipe/fixture.py"
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$demo_inline" "$demo_candidate" "$demo_before_commit" "$demo_after_commit" "$demo_destination" "$demo_fixture"
demo_python="$(command -v python3)"
"$demo_python" -I -B - "$demo_output" "$demo_before" "$demo_after" "$demo_api_binary" <<'IDENTITY'
import hashlib, json
from pathlib import Path
import sys
output = Path(sys.argv[1])
identities = {label: {"path": str(Path(value).resolve()), "sha256": hashlib.sha256(Path(value).read_bytes()).hexdigest()}
              for label, value in zip(("before", "after", "fixture"), sys.argv[2:])}
(output / "binaries.json").write_text(json.dumps(identities, indent=2) + "\n")
IDENTITY
demo_start_api "$demo_output/requests.jsonl"
cp "$demo_root/scripts/demos/list-pager/scene.py" "$demo_output/scene.py"
cp "$demo_fixture" "$demo_output/fixture.py"
cp "$demo_root/scripts/demos/capture_and_render.sh" "$demo_output/capture_and_render.sh"
cp "$demo_recipe/record.sh" "$demo_output/capture.sh"
cp "$demo_recipe/verify.py" "$demo_output/verify.py"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf 'Synthetic loopback API | terminal replay | 98 columns\n'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
demo_args=(files list --limit 1 --max-items -1)
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.2
if "$DEMO_PYTHON" "$DEMO_SCENE_DRIVER" openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '$ '
sleep 2
exit "$demo_status"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/shell-scene.sh"
{
  echo 'comparison: verified inline default versus pager-only default'
  echo 'before: viewer-switch-20261026; verified source snapshot and binary'
  echo 'after: pager-only-20261026; uncommitted candidate source'
  echo "before source HEAD: $demo_before_commit"
  echo "after source HEAD: $demo_after_commit"
  echo 'terminal: real PTY; 98x20; TERM=xterm-256color; TERM_PROGRAM=Apple_Terminal'
  echo 'color: COLORTERM=truecolor; COLORFGBG=15;0; no inherited credentials or settings'
  echo 'render: actual cast through agg swash; Menlo 18px; github-dark'
  echo 'data: two synthetic files; one record per API page; fake credentials; loopback only'
  echo 'scope: terminal replay; native Terminal.app access was unavailable'
  echo 'inputs: before Space; after Space, b, Space, q'
  echo 'native clipboard and operating-system scrolling: not claimed'
  demo_capture_metadata
  shasum -a 256 "$demo_output/capture.sh" "$demo_output/verify.py" "$demo_output/scene.py" "$demo_output/shell-scene.sh"
} > "$demo_output/metadata.txt"
demo_window_size=98x20
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme github-dark --fps-cap 20 --last-frame-duration 2)
for demo_version in before after; do
  mkdir -p "$demo_runtime/home-$demo_version"
  demo_label='Before: inline default; Space appends; final page exits'
  demo_actions='[{"marker":"page-001-item-001.jsonl","footer":"Space: more","key":" "}]'
  if [ "$demo_version" = after ]; then
    demo_label='After: pager default; Space forward; b back; q exits'
    demo_actions='[{"marker":"page-001-item-001.jsonl","footer":"q: quit","key":" "},{"marker":"page-002-item-001.jsonl","footer":"q: quit","key":"b"},{"marker":"page-001-item-001.jsonl","footer":"q: quit","key":" "},{"marker":"page-002-item-001.jsonl","footer":"q: quit","key":"q"}]'
  fi
  demo_requests_before="$(wc -l < "$demo_output/requests.jsonl")"
  demo_capture_scene "$demo_version" 0 "$demo_runtime/$demo_version" "$demo_api_url" "$demo_label" \
    "DEMO_PYTHON=$demo_python" "DEMO_SCENE_DRIVER=$demo_output/scene.py" \
    "DEMO_ASCIINEMA=$demo_asciinema" "DEMO_ACTIONS_JSON=$demo_actions" "DEMO_VERSION=$demo_version" \
    "DEMO_RELAY_EVIDENCE=$demo_output/$demo_version.relay.json" \
    "HOME=$demo_runtime/home-$demo_version" "XDG_CONFIG_HOME=$demo_runtime/home-$demo_version" \
    TERM_PROGRAM=Apple_Terminal COLORTERM=truecolor 'COLORFGBG=15;0' GOMAXPROCS=2 \
    OPENAI_CLI_NO_UPDATE_CHECK=1 OPENAI_CLI_NO_AUTO_UPDATE=1
  demo_requests_after="$(wc -l < "$demo_output/requests.jsonl")"
  demo_request_count=$((demo_requests_after - demo_requests_before))
  echo "$demo_version API requests: $demo_request_count (expected 2)" >> "$demo_output/metadata.txt"
  test "$demo_request_count" -eq 2
  "$demo_agg" --quiet "${demo_render_options[@]}" --select 1 \
    "$demo_output/$demo_version.cast" "$demo_output/$demo_version-first.gif"
  "$demo_ffmpeg" -hide_banner -loglevel error -y \
    -i "$demo_output/$demo_version-first.gif" -frames:v 1 "$demo_output/$demo_version-first.png"
done
demo_stop_api
demo_final_time="$("$demo_python" -I -B - "$demo_output" <<'FRAME'
import json
from pathlib import Path
import sys
output = Path(sys.argv[1])
cast = [json.loads(line) for line in (output/'after.cast').read_text().splitlines()]
relay = json.loads((output/'after.relay.json').read_text())
command_time = next(event[0] for event in cast[1:] if event[1] == 'o' and '$ openai' in event[2])
cutoff = command_time + relay['inputs'][-1]['time'] - 0.2
prefix = [cast[0], *[event for event in cast[1:] if event[0] <= cutoff]]
(output/'after-final-page.cast').write_text('\n'.join(json.dumps(item) for item in prefix)+'\n')
print(cutoff)
FRAME
)"
"$demo_asciinema" convert -f txt "$demo_output/after-final-page.cast" "$demo_output/after-final-page.txt"
"$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_final_time" \
  "$demo_output/after.cast" "$demo_output/after-final-page.gif"
"$demo_ffmpeg" -hide_banner -loglevel error -y \
  -i "$demo_output/after-final-page.gif" -frames:v 1 "$demo_output/after-final-page.png"
"$demo_python" -I -B "$demo_output/verify.py" "$demo_output"
demo_assemble_capture 200 before after
printf 'Recorded pager default replay: %s\n' "$demo_output"
