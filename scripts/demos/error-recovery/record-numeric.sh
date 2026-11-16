#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ]; then
  echo 'usage: record-numeric.sh BEFORE AFTER BEFORE_SHA AFTER_SHA OUTPUT_DIR normal|narrow' >&2
  exit 2
fi
root="$(cd "$(dirname "$0")/../../.." && pwd -P)"
before="$1"
after="$2"
before_source="$3"
after_source="$4"
output="$5"
profile="$6"
test "$before_source" = 18ae848748d96e9b7f0d57378f9d89b981277816
test "$(shasum -a 256 "$before" | awk '{print $1}')" = bdd261987f0eaeedf93568c726c2abeac3ac0ccee6fbc8e7d60d4bccbb44d418
test "$(git -C "$root" rev-parse HEAD)" = "$after_source"
test -z "$(git -C "$root" status --porcelain)"
after_hash="$(shasum -a 256 "$after" | awk '{print $1}')"
source "$root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$root" "$before" "$after" "$before_source" "$after_source" "$output" "$root/scripts/demos/error-recovery/scope_server.py"
case "$profile" in
  normal) demo_window_size=80x28; theme=dracula ;;
  narrow) demo_window_size=40x34; theme=solarized-light ;;
  *) echo 'choose normal or narrow' >&2; exit 2 ;;
esac
demo_render_options=(--font-family Menlo --font-size 18 --theme "$theme" --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
: > "$DEMO_STATUS_FILE"
cd "$HOME"
printf '%s\n\n' "$DEMO_SCENE_LABEL"
printf '$ openai files list --limit nope\n'
set +e
openai files list --limit nope
result=$?
set -e
printf 'invalid-option-value\t%s\n' "$result" >> "$DEMO_STATUS_FILE"
test "$result" -eq 1
printf '\n'
sleep 1
printf '%s\n' '$ printf '\''{"limit":"nope"}\n'\'' |' '> openai files list'
set +e
printf '{"limit":"nope"}\n' | openai files list
result=$?
set -e
printf 'invalid-piped-value\t%s\n' "$result" >> "$DEMO_STATUS_FILE"
test "$result" -eq 1
printf '\n'
sleep 1
printf '%s\n' '$ printf '\''{"limit":1}\n'\'' |' '> openai files list --format json'
set +e
printf '{"limit":1}\n' | openai files list --format json
result=$?
set -e
printf 'corrected-request\t%s\n' "$result" >> "$DEMO_STATUS_FILE"
test "$result" -eq 0
printf '\n'
sleep 2
SCENE
mkdir -p "$demo_runtime/home"
demo_capture_metadata > "$demo_output/metadata.txt"
printf 'Before source: %s\nAfter source: %s\nProfile: %s\nNO_COLOR=1; synthetic loopback terminal replay.\n' "$before_source" "$after_source" "$profile" >> "$demo_output/metadata.txt"
demo_start_api "$demo_output/requests.json"
printf 'invalid-option-value\t1\ninvalid-piped-value\t1\ncorrected-request\t0\n' > "$demo_runtime/expected-statuses.tsv"
for phase in before after; do
  demo_capture_scene "$phase" 0 "$demo_runtime/$phase" "$demo_api_url" "$phase: numeric input recovery" \
    HOME="$demo_runtime/home" XDG_CONFIG_HOME="$demo_runtime/home/config" NO_COLOR=1 GOMAXPROCS=2 OPENAI_UNTRUSTED_STDIN=false \
    DEMO_STATUS_FILE="$demo_output/$phase-statuses.tsv"
  cmp "$demo_runtime/expected-statuses.tsv" "$demo_output/$phase-statuses.tsv"
done
demo_stop_api
python3 - "$demo_output" <<'PY'
import json
from pathlib import Path
import re
import sys
root = Path(sys.argv[1])
counts = json.loads((root / "requests.json").read_text())
if counts != {"expected_gets": 2, "unexpected_requests": 0}:
    raise SystemExit("synthetic request counts differ: " + repr(counts))
texts = {}
for phase in ("before", "after"):
    text = (root / (phase + ".txt")).read_text()
    compact = re.sub(r"\s+", "", text)
    texts[phase] = compact
    for expected in (phase + ": numeric input recovery", "$ openai files list --limit nope",
                     "$ printf '{\"limit\":\"nope\"}\\n' |", "> openai files list",
                     "$ printf '{\"limit\":1}\\n' |", "> openai files list --format json",
                     "file_demo"):
        if re.sub(r"\s+", "", expected) not in compact:
            raise SystemExit(phase + " replay lost required content: " + repr(expected))
    if "[exit" in compact:
        raise SystemExit(phase + " replay contains an exit annotation")
    if "synthetic-private" in text:
        raise SystemExit(phase + " replay exposed a private fixture value")
checks = {
    "before": ("Invalid value for --limit. Expected an integer.",
               "The command could not be completed. Check your arguments with --help."),
    "after": ("Invalid value for --limit. Expected an integer, such as 1.",
              "Invalid piped input for --limit. Expected an integer, such as 1.",
              "Options and examples: openai files list --help"),
}
for phase, messages in checks.items():
    for message in messages:
        if re.sub(r"\s+", "", message) not in texts[phase]:
            raise SystemExit(phase + " replay lost required recovery: " + repr(message))
if "suchas1." in texts["before"]:
    raise SystemExit("before replay does not match the declared prior source")
PY
demo_assemble_capture 250 before after
test "$(git -C "$root" rev-parse HEAD)" = "$after_source"
test -z "$(git -C "$root" status --porcelain)"
test "$(shasum -a 256 "$after" | awk '{print $1}')" = "$after_hash"
test "$(shasum -a 256 "$before" | awk '{print $1}')" = bdd261987f0eaeedf93568c726c2abeac3ac0ccee6fbc8e7d60d4bccbb44d418
printf 'Request proof: exactly one successful synthetic GET per phase; zero unexpected requests.\nFinal source and executable hashes unchanged.\n' >> "$demo_output/metadata.txt"
