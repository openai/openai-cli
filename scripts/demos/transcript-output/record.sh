#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd -P)"
demo_root="${DEMO_REPO_ROOT:-$(cd "$demo_source/../../.." && pwd -P)}"
if [ ! -f "$demo_root/scripts/demos/capture_and_render.sh" ]; then
  echo 'Set DEMO_REPO_ROOT when running this recipe outside scripts/demos/transcript-output.' >&2
  exit 2
fi
source "$demo_root/scripts/demos/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
demo_python="$(command -v python3)"
test "$("$demo_asciinema" --version)" = 'asciinema 3.2.1'
test "$("$demo_agg" --version)" = 'agg 1.9.0'
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
demo_start_api "$demo_output/requests.jsonl"
printf 'synthetic audio fixture\n' > "$demo_runtime/sample.wav"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_FIXTURE_DIR" || exit 98
printf '\033[2J\033[H\033[?25l'
printf '%s\n' 'Terminal replay | synthetic loopback API'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.25
printf '%s\n' '$ openai audio transcribe --file sample.wav --model gpt-4o-transcribe-diarize --response-format diarized_json --chunking-strategy auto'
sleep 0.25
if openai audio transcribe --file sample.wav --model gpt-4o-transcribe-diarize \
  --response-format diarized_json --chunking-strategy auto; then
  demo_status=0
else
  demo_status=$?
fi
printf '\n$ '
sleep 2
exit "$demo_status"
SCENE

{
  echo 'feature: finite timestamped transcripts'
  echo "captured before commit: $demo_before_sha"
  echo "captured after commit: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo "before binary: $demo_before"
  echo "after binary: $demo_after"
  echo 'data: identical finite synthetic diarized JSON; two speakers; fake key; loopback only'
  echo 'capture: real PTY; stdin/stdout/stderr checked; isolated environment; bash --noprofile --norc'
  echo 'settings: xterm-256color; NO_COLOR=1; FORCE_COLOR=0; GOMAXPROCS=2'
  echo 'render: asciinema 3.2.1 + agg 1.9.0 resvg; Menlo 22px; Dracula; 90 columns x 30 rows; line height 1.2'
  echo 'scope: terminal replay; no native Terminal app, Windows, Linux, or live service claim'
  echo 'model: documented specialized example; announced retirement February 26, 2027'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/server.py" \
  "$demo_root/scripts/demos/capture_and_render.sh" > "$demo_output/source-sha256.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
cp "$demo_source/record.sh" "$demo_output/record.sh"
cp "$demo_source/server.py" "$demo_output/server.py"

demo_window_size=90x30
demo_render_options=(--renderer resvg --font-family Menlo --font-size 22 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
for demo_scene in before after; do
  demo_command_dir="$demo_runtime/after"
  demo_label='After: timestamped speaker lines'
  if [ "$demo_scene" = before ]; then
    demo_command_dir="$demo_runtime/before"
    demo_label='Before: transcript and nested segment fields'
  fi
  demo_capture_scene "$demo_scene" 0 "$demo_command_dir" "$demo_api_url" "$demo_label" \
    FORCE_COLOR=0 NO_COLOR=1 GOMAXPROCS=2 "DEMO_FIXTURE_DIR=$demo_runtime"
done
demo_stop_api
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B - "$demo_output" <<'PY' > "$demo_output/validation.txt"
import hashlib
import json
import pathlib
import sys

output = pathlib.Path(sys.argv[1])
before = (output / "before.txt").read_text(encoding="utf-8")
after = (output / "after.txt").read_text(encoding="utf-8")
assert "Thanks for calling. I need help." in before, before
assert "Segments:" in before and "Start: 0" in before and "End: 5.2" in before, before
assert "[00:00.000–00:05.200] A: Thanks for calling." in after, after
assert "[00:05.200–00:12.800] B: I need help." in after, after
for phrase in ("Thanks for calling.", "I need help."):
    assert before.count(phrase) == 2, before
    assert after.count(phrase) == 1, after
for marker in ("demo_seg_001", "demo_seg_002", "transcript.text.segment", "12.8", "transcribe"):
    assert marker in before and marker in after, marker
requests = [json.loads(line) for line in (output / "requests.jsonl").read_text().splitlines()]
expected = {
    "file": {"filename": "sample.wav", "bytes": 24,
             "sha256": hashlib.sha256(b"synthetic audio fixture\n").hexdigest()},
    "model": "gpt-4o-transcribe-diarize", "response_format": "diarized_json", "chunking_strategy": "auto",
}
assert len(requests) == 2 and requests[0] == requests[1], requests
assert all(item["method"] == "POST" and item["path"] == "/v1/audio/transcriptions"
           and item["status"] == 200 and item["body"] == expected for item in requests), requests
for scene in ("before", "after"):
    capture = [json.loads(line) for line in (output / (scene + ".cast")).read_text().splitlines()]
    assert capture[0]["width"] == 90 and capture[0]["height"] == 30, capture[0]
    text = "".join(event[2] for event in capture[1:] if event[1] == "o")
    assert "Terminal replay | synthetic loopback API" in text
    assert text.count("$ openai audio transcribe") == 1
    assert text.endswith("\r\n$ "), "missing final shell prompt"
print("PASS: identical validated synthetic requests and response bytes; both process exits zero.")
print("PASS: baseline nested fields; candidate timestamped speakers; transcript appears once after the change.")
print("PASS: segment IDs, types, duration, and task remain visible; both recordings use 90x30 PTYs.")
PY
demo_assemble_capture 200 before after
printf 'Recorded transcript terminal replay in %s\n' "$demo_output"
