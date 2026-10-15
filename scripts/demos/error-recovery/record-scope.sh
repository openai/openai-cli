#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record-scope.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR normal|narrow' >&2
  exit 2
fi
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
source "$root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$root" "$1" "$2" "$3" "$4" "$5" "$root/scripts/demos/error-recovery/scope_server.py"
case "$6" in
  normal) demo_window_size=80x24; theme=dracula ;;
  narrow) demo_window_size=40x30; theme=solarized-light ;;
  *) echo 'choose normal or narrow' >&2; exit 2 ;;
esac
demo_render_options=(--font-family Menlo --font-size 18 --theme "$theme" --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
: > "$DEMO_STATUS_FILE"
cd "$HOME"
printf '%s\n\n' "$DEMO_SCENE_LABEL"
printf '$ openai files --limit 1 list\n'
set +e
openai files --limit 1 list
result=$?
set -e
printf 'unavailable-option\t%s\n' "$result" >> "$DEMO_STATUS_FILE"
test "$result" -eq 1
printf '\n'
printf '$ openai files list --limit 1 --format json\n'
set +e
openai files list --limit 1 --format json
result=$?
set -e
printf 'corrected-request\t%s\n' "$result" >> "$DEMO_STATUS_FILE"
test "$result" -eq 0
printf '\n'
sleep 1
SCENE
mkdir -p "$demo_runtime/home"
demo_capture_metadata > "$demo_output/metadata.txt"
printf 'Before source: %s\nAfter source: %s\nProfile: %s\nNO_COLOR=1; terminal replay; loopback fixture.\n' "$3" "$4" "$6" >> "$demo_output/metadata.txt"
demo_start_api "$demo_output/requests.json"
printf 'unavailable-option\t1\ncorrected-request\t0\n' > "$demo_runtime/expected-statuses.tsv"
for phase in before after; do
  demo_capture_scene "$phase" 0 "$demo_runtime/$phase" "$demo_api_url" "$phase: option scope (terminal replay)" \
    HOME="$demo_runtime/home" NO_COLOR=1 GOMAXPROCS=2 DEMO_STATUS_FILE="$demo_output/$phase-statuses.tsv"
  grep -F 'file_demo' "$demo_output/$phase.txt"
  cmp "$demo_runtime/expected-statuses.tsv" "$demo_output/$phase-statuses.tsv"
  if grep -qF '[exit' "$demo_output/$phase.txt"; then exit 1; fi
done
grep -F 'An option is not recognized.' "$demo_output/before.txt"
# Compare the normalized text so native wrapping does not obscure the assertion.
tr '\n' ' ' < "$demo_output/after.txt" | grep -E 'The --limit option is not available.*for this command\.'
demo_stop_api
python3 - "$demo_output/requests.json" <<'PY'
import json
import sys
with open(sys.argv[1]) as source:
    counts = json.load(source)
if counts != {"expected_gets": 2, "unexpected_requests": 0}:
    raise SystemExit("synthetic request counts differ: " + repr(counts))
PY
demo_assemble_capture 250 before after
