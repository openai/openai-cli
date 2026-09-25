#!/bin/bash
set -euo pipefail

if [ "$#" -ne 6 ]; then
  echo "usage: record.sh FEATURE BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR" >&2
  exit 2
fi
demo_feature="$1"
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
case "$demo_feature" in readable-output|helpful-errors|list-get-results) ;; *) exit 2;; esac
demo_prepare_capture "$demo_root" "$2" "$3" "$4" "$5" "$6" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/demo-api}"
demo_start_api
# Retain the original VHS recipe for later use in a permitted environment.
cp "$demo_source/$demo_feature.tape" "$demo_output/comparison.tape"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | asciinema + agg'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.5
case "$DEMO_REQUEST" in
  files-list) demo_args=(files list --max-items 2);;
  files-retrieve) demo_args=(files retrieve file_training);;
  *) demo_args=(models retrieve --model "$DEMO_MODEL");;
esac
case "$DEMO_FORMAT" in
  json) demo_args=(--format json "${demo_args[@]}");;
  error-json) demo_args=(--format-error json "${demo_args[@]}");;
esac
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.4
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3.5
exit "$demo_status"
SCENE

demo_scenes=(before after explicit-json)
demo_request=model
if [ "$demo_feature" = readable-output ]; then
  demo_model=demo-chat
  demo_expected_status=0
  demo_before_label='before: main | synthetic local response'
  demo_after_label='after: readable output | same synthetic response'
  demo_json_format=json
elif [ "$demo_feature" = helpful-errors ]; then
  demo_model=demo-auth
  demo_expected_status=1
  demo_before_label='before: readable-output parent | synthetic 401 error'
  demo_after_label='after: helpful errors | same synthetic 401 error'
  demo_json_format=error-json
else
  demo_model=""
  demo_request=files-list
  demo_expected_status=0
  demo_before_label='Before: pager cleanup parent | two synthetic files'
  demo_after_label='After: list/get summaries | same two files'
  demo_json_format=json
  demo_scenes=(before after before-retrieve after-retrieve explicit-json)
fi
{
  echo "feature: $demo_feature"
  echo "comparison base / captured before commit: $demo_before_sha"
  echo "proposed feature / captured after commit: $demo_after_sha"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo "data: fixed synthetic responses from local demo-api; fake key; no live OpenAI requests"
  echo "capture: real PTY, stdin/stdout/stderr verified as terminals, bash --noprofile --norc"
  echo "render: each scene replayed with asciinema + agg swash, then full GIF frames joined by ffmpeg; Menlo 22px, Dracula, 90 columns x 24 rows, line height 1.2, maximum 20 fps"
  echo "scope: no native Apple Terminal, PowerShell, or cmd.exe capture"
  echo "recipe: record.sh; comparison.tape is the preserved VHS recipe, not the renderer used"
  demo_capture_metadata
  shasum -a 256 "$demo_source/record.sh" "$demo_source/main.go"
} > "$demo_output/metadata.txt"

demo_window_size=90x24
demo_render_options=(--font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3.5)
for demo_scene in "${demo_scenes[@]}"; do
  demo_command_dir="$demo_runtime/after"
  demo_format=default
  demo_scene_request="$demo_request"
  case "$demo_scene" in
    before) demo_command_dir="$demo_runtime/before"; demo_label="$demo_before_label";;
    after) demo_label="$demo_after_label";;
    before-retrieve) demo_command_dir="$demo_runtime/before"; demo_scene_request=files-retrieve; demo_label='Before: pager cleanup parent | retrieve the first file';;
    after-retrieve) demo_scene_request=files-retrieve; demo_label='After: list/get summaries | same file';;
    explicit-json) demo_format="$demo_json_format"; demo_label='explicit JSON | same synthetic API response';;
  esac
  if [ "$demo_feature" = list-get-results ] && [ "$demo_scene" = explicit-json ]; then
    demo_scene_request=files-retrieve
    demo_label='Explicit JSON | full data for the same file'
  fi
  demo_capture_scene "$demo_scene" "$demo_expected_status" "$demo_command_dir" "$demo_api_url" "$demo_label" \
    "DEMO_MODEL=$demo_model" "DEMO_FORMAT=$demo_format" "DEMO_REQUEST=$demo_scene_request"
  demo_expected_text=demo-project
  if [ "$demo_feature" = helpful-errors ]; then
    demo_expected_text=invalid_api_key
    [ "$demo_scene" != after ] || demo_expected_text='help setup'
  elif [ "$demo_feature" = list-get-results ]; then
    demo_expected_text=file_training
  fi
  if ! /usr/bin/grep -Fq "$demo_expected_text" "$demo_output/$demo_scene.txt"; then
    echo "Unexpected $demo_scene output: missing $demo_expected_text" >&2
    exit 1
  fi
  if [ "$demo_feature" = list-get-results ]; then
    case "$demo_scene" in
      before|after) /usr/bin/grep -Fq file_reference "$demo_output/$demo_scene.txt";;
    esac
    case "$demo_scene" in
      before|before-retrieve) /usr/bin/grep -Fq 'Created at: 1704067200' "$demo_output/$demo_scene.txt";;
      after|after-retrieve)
        test "$(/usr/bin/grep -Fc 'Summary; use --format json for full data.' "$demo_output/$demo_scene.txt")" -eq 1
        if /usr/bin/grep -Fq 'Created at:' "$demo_output/$demo_scene.txt"; then
          echo "Unexpected $demo_scene output: creation timestamp was not summarized" >&2
          exit 1
        fi
        ;;
      explicit-json) /usr/bin/grep -Fq '"created_at": 1704067200' "$demo_output/$demo_scene.txt";;
    esac
  fi
done
demo_stop_api
demo_assemble_capture 350 "${demo_scenes[@]}"
printf 'Recorded %s terminal replay in %s\n' "$demo_feature" "$demo_output"
