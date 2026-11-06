#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-update-notice.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_python="$(command -v python3)"
demo_source_manifest="${DEMO_SOURCE_MANIFEST:-}"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size=80x28
demo_render_options=(--renderer resvg --font-family Menlo --font-size 20 --line-height 1.3 \
  --theme asciinema --fps-cap 20 --last-frame-duration 3)

# Version commands cannot enter the notice path. Keep their environment isolated too.
mkdir -p "$demo_runtime/version-home"
for demo_side in before after; do
  env -i PATH=/usr/bin:/bin HOME="$demo_runtime/version-home" \
    OPENAI_CLI_NO_UPDATE_CHECK=1 "$demo_runtime/$demo_side/openai" --version \
    > "$demo_output/$demo_side-version.txt"
done
"$demo_python" -I -B "$demo_source/update-notice/validate.py" seed "$demo_runtime" "$demo_output"
demo_start_api "$demo_runtime/requests.txt"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_WEBHOOK_SECRET
cd "$DEMO_WORKING_DIRECTORY"
printf '\033[2J\033[H$ openai\n'
sleep 0.4
if openai; then demo_status=0; else demo_status=$?; fi
printf '\n# %s\n$ ' "$DEMO_SCENE_LABEL"
sleep 3
exit "$demo_status"
SCENE
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
{
  echo 'feature: one optional update notice after bare openai'
  echo "before commit: $demo_before_sha"
  if [ -n "$demo_source_manifest" ]; then
    cp "$demo_source_manifest" "$demo_output/source-manifest.txt"
    echo "after source parent: $demo_after_sha"
    echo 'The parent alone does not identify uncommitted source. Use source-manifest.txt and binary hashes.'
    shasum -a 256 "$demo_output/source-manifest.txt"
  else
    echo "after commit: $demo_after_sha"
  fi
  echo 'command: openai; version commands run separately before cache preparation'
  echo 'fixtures: fresh private release caches; no GitHub release request is due'
  echo 'API: no credentials; rejecting loopback fixture requires zero API requests'
  echo 'capture: real binaries, isolated Bash PTYs, temporary HOME/XDG paths, restricted PATH'
  echo 'render: asciinema + agg resvg, Menlo20px, asciinema theme, 80x28, line height1.3'
  echo 'viewport: normal terminal scrolling; full output remains in casts and transcripts'
  echo 'captions: scene script prints the final shell comment; CLI output is unchanged'
  echo 'comparison: repeated CR immediately before LF collapses to one CR; other bytes must match'
  echo 'raw evidence: original casts and CLI tty files retain all captured newline bytes'
  echo 'scope: terminal replay; not native graphical terminal or font validation'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-update-notice.sh" \
    "$demo_source/update-notice/validate.py" "$demo_source/update-notice/README.md" \
    "$demo_source/image-models/main.go"
} > "$demo_output/metadata.txt"

for demo_scene in before after repeat current; do
  demo_side=after
  demo_state_name=after
  case "$demo_scene" in
    before) demo_side=before; demo_state_name=before; demo_label='BEFORE: newer release cached';;
    after) demo_label='AFTER: newer release cached';;
    repeat) demo_label='AFTER: repeated run, same cache';;
    current) demo_state_name=current; demo_label='AFTER: installed version is current';;
  esac
  demo_state="$demo_runtime/$demo_state_name-state"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_side" \
    "$demo_api_url/help-only/v1" "$demo_label" \
    "HOME=$demo_state/home" "USERPROFILE=$demo_state/home" \
    "XDG_CONFIG_HOME=$demo_state/config" "XDG_CACHE_HOME=$demo_state/cache" \
    "XDG_DATA_HOME=$demo_state/data" "GOENV=off" \
    "DEMO_WORKING_DIRECTORY=$demo_state/work" \
    'COLORFGBG=15;0' 'COLORTERM=truecolor' 'LC_ALL=en_US.UTF-8' 'GOMAXPROCS=2'
  "$demo_python" -I -B "$demo_source/update-notice/validate.py" snapshot \
    "$demo_runtime" "$demo_output" "$demo_state_name" "$demo_scene"
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
"$demo_python" -I -B "$demo_source/update-notice/validate.py" check "$demo_output" \
  | tee "$demo_output/validation.txt"
demo_assemble_capture 300 before after repeat current
for demo_scene in before after repeat current; do
  # Select the completed replay's final frame, including the CLI footer.
  demo_frames="$("$demo_ffprobe" -v error -select_streams v:0 \
    -show_entries stream=nb_frames -of csv=p=0 "$demo_output/$demo_scene.gif")"
  [[ "$demo_frames" =~ ^[1-9][0-9]*$ ]]
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene.gif" \
    -vf "select=eq(n\\,$((demo_frames-1)))" -frames:v 1 "$demo_output/$demo_scene.png"
done
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded update notice comparison in %s\n' "$demo_output"
