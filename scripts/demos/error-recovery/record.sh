#!/bin/bash
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
source "$root/scripts/demos/capture_and_render.sh"
# These scenes fail locally, so they require no API helper.
demo_prepare_capture "$root" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
demo_window_size=100x23
demo_render_options=(--font-family Menlo --font-size 18 --theme dracula --line-height 1.2 --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -u
: > "$DEMO_STATUS_FILE" || exit 1
printf '%s\n\n' "$DEMO_SCENE_LABEL"
cd "$DEMO_ISOLATED_HOME"
run_case() {
  case_name="$1"
  expected="$2"
  shift 2
  printf '$ openai %s\n' "$*"
  openai "$@"
  actual=$?
  printf '%s\t%s\n' "$case_name" "$actual" >> "$DEMO_STATUS_FILE" || exit 1
  test "$actual" -eq "$expected" || exit 1
  printf '\n'
}
run_case command-typo 3 modles list
run_case help-typo 3 help audio transcriptons create
run_case missing-upload 1 files upload missing.txt --purpose user_data
run_case missing-part 1 uploads parts create --upload-id upload_demo --data missing.bin
sleep 1
SCENE
mkdir -p "$demo_runtime/home"
demo_capture_metadata > "$demo_output/metadata.txt"
printf 'Before source: %s\nAfter source: %s\nTerminal replay; synthetic local input failures; no API requests.\n' "$3" "$4" >> "$demo_output/metadata.txt"
printf 'command-typo\t3\nhelp-typo\t3\nmissing-upload\t1\nmissing-part\t1\n' > "$demo_runtime/expected-statuses.tsv"
for phase in before after; do
  demo_capture_scene "$phase" 0 "$demo_runtime/$phase" http://127.0.0.1:1 \
    "$phase: actionable errors (terminal replay)" \
    HOME="$demo_runtime/home" DEMO_ISOLATED_HOME="$demo_runtime/home" NO_COLOR=1 GOMAXPROCS=2 \
    DEMO_STATUS_FILE="$demo_output/$phase-statuses.tsv"
  cmp "$demo_runtime/expected-statuses.tsv" "$demo_output/$phase-statuses.tsv"
  if grep -qF '[exit' "$demo_output/$phase.txt"; then exit 1; fi
  if grep -E 'synthetic-private-|authorization|Traceback|panic:' "$demo_output/$phase.txt"; then
    exit 1
  fi
done
grep -F 'Unknown help topic.' "$demo_output/before.txt"
grep -F 'Did you mean: openai help models list?' "$demo_output/after.txt"
grep -F 'Did you mean: openai help audio transcriptions create?' "$demo_output/after.txt"
grep -F 'the file for --data.' "$demo_output/after.txt"
demo_assemble_capture 250 before after
