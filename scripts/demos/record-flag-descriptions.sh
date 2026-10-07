#!/bin/bash
# Record help and error recovery at 90 and 56 columns. Keep all media outside Git.
# Set DEMO_INCLUDE_FULL=0 to omit the longer reference pages.
# Build the existing fixture before running:
#   go build -o /tmp/image-model-demo-api ./scripts/demos/image-models
#   DEMO_API_BINARY=/tmp/image-model-demo-api scripts/demos/record-flag-descriptions.sh \
#     BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-flag-descriptions.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_full_rows="${DEMO_FULL_ROWS:-280}"
[[ "$demo_full_rows" =~ ^[1-9][0-9]*$ ]] || { echo 'DEMO_FULL_ROWS must be positive.' >&2; exit 2; }
demo_include_full="${DEMO_INCLUDE_FULL:-1}"
[[ "$demo_include_full" =~ ^[01]$ ]] || { echo 'DEMO_INCLUDE_FULL must be 0 or 1.' >&2; exit 2; }
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_output_root="$demo_output"
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
demo_start_api "$demo_runtime/requests.txt"
{
  echo 'feature: useful help, typed flags, and specific error recovery'
  echo "base commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'data: no API key; loopback fixture records unexpected requests'
  echo 'capture: real binaries in isolated bash PTYs; no personal environment or shell hooks'
  echo 'scope: terminal replay; no native graphical terminal claim'
  demo_capture_metadata
  shasum -a 256 "$demo_source/record-flag-descriptions.sh" "$demo_source/image-models/main.go"
} > "$demo_output_root/metadata.txt"

demo_topics=(root-short models-short responses-short images-short unknown-option invalid-integer)
if [ "$demo_include_full" -eq 1 ]; then demo_topics+=(root-full images-full); fi
for demo_topic in "${demo_topics[@]}"; do
  demo_expected_status=0
  case "$demo_topic" in
    root-full) demo_args=(help --all); demo_rows="$demo_full_rows";;
    images-full) demo_args=(help --all images generate); demo_rows="$demo_full_rows";;
    root-short) demo_args=(--help); demo_rows=48;;
    models-short) demo_args=(models list --help); demo_rows=48;;
    responses-short) demo_args=(responses create --help); demo_rows=48;;
    images-short) demo_args=(images generate --help); demo_rows=48;;
    unknown-option) demo_args=(models list --max-itmes=1); demo_rows=20; demo_expected_status=1;;
    invalid-integer) demo_args=(models list --max-items=not-an-integer); demo_rows=20; demo_expected_status=1;;
  esac
  {
    cat <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.3
SCENE
    # Preserve exact arguments without eval or personal shell expansion.
    printf 'demo_args=('
    printf ' %q' "${demo_args[@]}"
    printf ' )\n'
    cat <<'SCENE'
printf '$ openai'
printf ' %s' "${demo_args[@]}"
printf '\n'
sleep 0.3
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
  } > "$demo_runtime/scene.sh"

  for demo_columns in 90 56; do
    demo_window_size="${demo_columns}x${demo_rows}"
    demo_output="$demo_output_root/$demo_topic-$demo_columns"
    mkdir "$demo_output"
    {
      printf 'command: openai'; printf ' %s' "${demo_args[@]}"; printf '\n'
      echo "render: asciinema + agg, Menlo 18px, Dracula, $demo_window_size, line height 1.2"
    } > "$demo_output/metadata.txt"
    cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
    demo_capture_scene before "$demo_expected_status" "$demo_runtime/before" "$demo_api_url/help-only/v1" \
      "Before: $demo_topic ($demo_columns columns)" "COLUMNS=$demo_columns" "LINES=$demo_rows"
    demo_capture_scene after "$demo_expected_status" "$demo_runtime/after" "$demo_api_url/help-only/v1" \
      "After: $demo_topic ($demo_columns columns)" "COLUMNS=$demo_columns" "LINES=$demo_rows"
    for demo_scene in before after; do
      /usr/bin/grep -Fq 'openai' "$demo_output/$demo_scene.txt"
      if /usr/bin/grep -Eq 'synthetic-demo-key|/help-only/v1' "$demo_output/$demo_scene.txt"; then
        echo 'The command disclosed a configured fixture value.' >&2
        exit 1
      fi
    done
    test ! -s "$demo_runtime/requests.txt"
    demo_assemble_capture 300 before after
  done
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output_root/requests.txt"
test ! -s "$demo_output_root/requests.txt"
echo 'API requests: 0 (required)' >> "$demo_output_root/metadata.txt"
printf 'Recorded help and error recovery in %s\n' "$demo_output_root"
