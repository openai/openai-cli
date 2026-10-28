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
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/global-flags-demo-api}"
demo_start_api "$demo_runtime/requests.jsonl"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '\033[1m%s\033[0m\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' "$DEMO_SCENE_SUMMARY"
sleep 0.5
for demo_position in root group leaf; do
  case "$demo_position" in
    root) demo_args=(--project proj-example models list --transform=id -r)
      demo_position_label='1. Before the command';;
    group) demo_args=(models --project proj-example list --transform=id -r)
      demo_position_label='2. Between command words';;
    leaf) demo_args=(models list --project proj-example --transform=id -r)
      demo_position_label='3. After the command';;
  esac
  printf '%s\n' "$demo_position_label"
  printf '$ openai'
  printf ' %s' "${demo_args[@]}"
  printf '\n'
  sleep 0.4
  if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
  printf '%s\t%s\t%s\n' "$DEMO_SCENE" "$demo_position" "$demo_status" >> "$DEMO_STATUS_LOG" || exit 98
  demo_expected=0
  if [ "$DEMO_SCENE" = before ] && [ "$demo_position" != root ]; then demo_expected=1; fi
  if [ "$demo_status" -ne "$demo_expected" ]; then exit 97; fi
  printf '\n'
  sleep 1
done
printf '%s\n' 'Synthetic API: output shows the received project header.'
sleep 4
SCENE

{
  echo 'feature: consistent global flag placement'
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "proposed feature / captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: loopback synthetic model IDs reflect validated project headers; no live requests'
  echo 'capture: real PTY, stdin/stdout/stderr verified as terminals, bash --noprofile --norc'
  echo 'output: existing --transform=id -r extracts the synthetic model ID; output is not rewritten'
  echo 'render: asciinema + agg resvg, Menlo 26px, asciinema theme, plain labels, 80 columns x 18 rows, line height 1.3, maximum 20 fps'
  echo 'scope: terminal replay; not native Apple Terminal, PowerShell, or cmd.exe validation'
  echo 'each scene checks actual command statuses; statuses.tsv retains every result'
  echo 'recipe: scripts/demos/global-flags/record.sh'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/main.go" "$demo_source/validate.py"
} > "$demo_output/metadata.txt"

demo_window_size=80x18
demo_render_options=(--renderer resvg --font-family Menlo --font-size 26 --line-height 1.3 \
  --theme asciinema --fps-cap 20 --last-frame-duration 4)
for demo_scene in before after; do
  demo_command_dir="$demo_runtime/$demo_scene"
  demo_label='BEFORE'
  demo_summary='--project works only before the command.'
  if [ "$demo_scene" = after ]; then
    demo_label='AFTER'
    demo_summary='--project works in all three positions.'
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    "DEMO_SCENE=$demo_scene" "DEMO_SCENE_SUMMARY=$demo_summary" "DEMO_STATUS_LOG=$demo_runtime/statuses.tsv"
done
demo_stop_api
cp "$demo_runtime/requests.jsonl" "$demo_output/requests.jsonl"
cp "$demo_runtime/statuses.tsv" "$demo_output/statuses.tsv"
"$demo_python" "$demo_source/validate.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 400 before after
printf 'Recorded global-flags terminal replays in %s\n' "$demo_output"
