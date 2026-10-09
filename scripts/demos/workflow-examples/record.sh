#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record.sh --run-authorized-pty-slot BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
: "${DEMO_BEFORE_SHA256:?Set the independently verified baseline binary SHA256}"
: "${DEMO_AFTER_SHA256:?Set the independently verified candidate binary SHA256}"
: "${DEMO_SOURCE_MANIFEST:?Set the source and binary provenance manifest path}"
test -f "$DEMO_SOURCE_MANIFEST"
record_source="$(cd "$(dirname "$0")" && pwd -P)"
record_repo="$(cd "$record_source/../../.." && pwd -P)"
source "$record_repo/scripts/demos/capture_and_render.sh"
# The shared helper requires an executable fixture path. No fixture starts here.
demo_prepare_capture "$record_repo" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
python3 - "$demo_before" "$DEMO_BEFORE_SHA256" "$demo_after" "$DEMO_AFTER_SHA256" <<'PY'
import hashlib
from pathlib import Path
import re
import sys
for path, expected in zip(sys.argv[1::2], sys.argv[2::2]):
    if not re.fullmatch(r"[0-9a-f]{64}", expected):
        raise SystemExit("Expected a lowercase SHA256 for each binary")
    if hashlib.sha256(Path(path).read_bytes()).hexdigest() != expected:
        raise SystemExit("Binary differs from its verified source manifest: " + path)
PY
cp "$record_source/scene.sh" "$demo_runtime/scene.sh"
cp "$DEMO_SOURCE_MANIFEST" "$demo_output/source-manifest.txt"
cp "$record_source/scene.sh" "$record_source/record.sh" "$record_source/validate.py" "$demo_output/"
{
  echo 'feature: offline runnable workflow examples'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo "before verified SHA256: $DEMO_BEFORE_SHA256"
  echo "after verified SHA256: $DEMO_AFTER_SHA256"
  echo 'capture: isolated Bash PTYs; no API credentials; no workflow commands executed'
  echo 'network: baseline loopback-only configuration; candidate invalid remote configuration; no API fixture'
  echo 'scope: asciinema/agg terminal replay; no graphical terminal or font validation'
  echo 'render: 80x28 normal; 40x34 narrow; NO_COLOR; Menlo 18px; asciinema theme'
  demo_capture_metadata
  shasum -a 256 "$record_source/record.sh" "$record_source/scene.sh" "$record_source/validate.py" \
    "$demo_output/source-manifest.txt"
} > "$demo_output/metadata.txt"
demo_window_size=80x28
demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme asciinema --fps-cap 15 --last-frame-duration 3)
mkdir -p "$demo_runtime/before-home" "$demo_runtime/after-home"
demo_capture_scene before 3 "$demo_runtime/before" http://127.0.0.1:1 'BEFORE: workflow examples unavailable' \
  "HOME=$demo_runtime/before-home" DEMO_TOPIC=files NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2
for demo_topic in files audio models; do
  demo_scene="after-$demo_topic"
  if [ "$demo_topic" = files ]; then demo_scene=after; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/after" '://offline-examples-demo' "AFTER: $demo_topic workflow" \
    "HOME=$demo_runtime/after-home" "DEMO_TOPIC=$demo_topic" NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
    OPENAI_MTLS_CLIENT_CERT_FILE=/missing-demo-cert OPENAI_MTLS_CLIENT_KEY_FILE=/missing-demo-key
  # pipefail preserves the CLI status. The recipes are printed, never evaluated.
  if env -i PATH="$demo_runtime/after:/usr/bin:/bin" LANG=en_US.UTF-8 \
    HOME="$demo_runtime/after-home" TERM=dumb NO_COLOR=1 GOMAXPROCS=2 \
    OPENAI_BASE_URL='://offline-examples-demo' \
    OPENAI_MTLS_CLIENT_CERT_FILE=/missing-demo-cert OPENAI_MTLS_CLIENT_KEY_FILE=/missing-demo-key \
    "$demo_after" examples "$demo_topic" 2> "$demo_output/$demo_topic.stderr" \
    | cat > "$demo_output/$demo_topic.stdout"; then demo_pipe_status=0; else demo_pipe_status=$?; fi
  echo "$demo_topic pipe exit status: $demo_pipe_status (expected 0)" >> "$demo_output/metadata.txt"
  test "$demo_pipe_status" -eq 0
  test ! -s "$demo_output/$demo_topic.stderr"
done
demo_window_size=40x34
demo_capture_scene narrow 0 "$demo_runtime/after" '://offline-examples-demo' 'AFTER: files at 40 columns' \
  "HOME=$demo_runtime/after-home" DEMO_TOPIC=files NO_COLOR=1 FORCE_COLOR=0 GOMAXPROCS=2 \
  OPENAI_MTLS_CLIENT_CERT_FILE=/missing-demo-cert OPENAI_MTLS_CLIENT_KEY_FILE=/missing-demo-key
python3 "$record_source/validate.py" "$demo_output" "$demo_runtime/after-home"
demo_assemble_capture 300 before after after-audio after-models
python3 - "$demo_before" "$DEMO_BEFORE_SHA256" "$demo_after" "$DEMO_AFTER_SHA256" <<'PY'
import hashlib
from pathlib import Path
import sys
for path, expected in zip(sys.argv[1::2], sys.argv[2::2]):
    if hashlib.sha256(Path(path).read_bytes()).hexdigest() != expected:
        raise SystemExit("Binary changed during capture: " + path)
PY
printf 'Recorded workflow examples in %s\n' "$demo_output"
