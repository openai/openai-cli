#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo "usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_start_api "$demo_runtime/requests.txt"
cp "$demo_source/image-models.tape" "$demo_output/comparison.tape"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg | synthetic metadata only'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.5
demo_args=(images models --all)
if [ "$DEMO_SCENE" = offline ]; then
  unset OPENAI_API_KEY
  demo_args=(images models --offline)
fi
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.4
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n[exit %s]\n$ ' "$demo_status"
sleep 4
exit "$demo_status"
SCENE

{
  echo 'feature: exact image model names'
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "proposed feature / captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: fixed loopback metadata; synthetic key; no image generation or live requests'
  echo 'capture: real PTY, stdin/stdout/stderr verified as terminals, bash --noprofile --norc'
  echo 'render: asciinema + agg, Menlo 22px, Dracula, 110 columns x 34 rows, line height 1.2, maximum 20 fps'
  echo 'scope: terminal replay; not native Apple Terminal, PowerShell, or cmd.exe validation'
  echo 'comparison.gif includes before and after; offline and partial are supplemental scenes'
  echo 'recipe: record.sh; comparison.tape is a retained VHS recipe, not the renderer used'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/main.go" "$demo_source/image-models.tape"
} > "$demo_output/metadata.txt"

demo_window_size=110x34
demo_render_options=(--font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 4)
for demo_scene in before after offline partial; do
  demo_command_dir="$demo_runtime/after"
  demo_endpoint="$demo_api_url/normal/v1"
  demo_expected_status=0
  demo_expected_requests=12
  case "$demo_scene" in
    before)
      demo_command_dir="$demo_runtime/before"
      demo_label='Before: main | image model discovery is not available'
      demo_expected_status=1; demo_expected_requests=0;;
    after) demo_label='After: exact names, visibility, retirement and dated versions';;
    offline)
      demo_label='Offline: known model names | no API key and zero requests'
      demo_expected_requests=0;;
    partial)
      demo_label='Partial results: one synthetic 503 | completed checks are kept'
      demo_endpoint="$demo_api_url/partial/v1"; demo_expected_status=1;;
  esac
  demo_requests_before="$(wc -l < "$demo_runtime/requests.txt")"
  demo_capture_scene "$demo_scene" "$demo_expected_status" "$demo_command_dir" "$demo_endpoint" "$demo_label" \
    "DEMO_SCENE=$demo_scene"
  demo_requests_after="$(wc -l < "$demo_runtime/requests.txt")"
  demo_request_count=$((demo_requests_after - demo_requests_before))
  echo "$demo_scene metadata requests: $demo_request_count (expected $demo_expected_requests)" >> "$demo_output/metadata.txt"
  test "$demo_request_count" -eq "$demo_expected_requests"
  case "$demo_scene" in
    before) demo_expected_text='models';;
    after) demo_expected_text='Retired 2000-01-01';;
    offline) demo_expected_text='Not checked';;
    partial) demo_expected_text='Could not check';;
  esac
  if ! /usr/bin/grep -Fq "$demo_expected_text" "$demo_output/$demo_scene.txt"; then
    echo "Unexpected $demo_scene output: missing $demo_expected_text" >&2
    exit 1
  fi
done
# Finish the request log before copying it or reporting a successful recording.
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
demo_assemble_capture 400 before after
printf 'Recorded image-model terminal replays in %s\n' "$demo_output"
