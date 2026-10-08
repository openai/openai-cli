#!/bin/bash
# Reuse the shared capture lifecycle for delayed image retry scenes.
set -euo pipefail
if [ "$#" -ne 5 ]; then
  echo 'usage: image_friendly_loading_record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
source "$demo_source/capture_and_render.sh"
demo_driver="$demo_source/image_friendly_loading_check.py"
demo_python="$(command -v python3)"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_python"
demo_window_size=104x28
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 --theme dracula --fps-cap 20 --last-frame-duration 2)
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
demo_args=("$DEMO_BINARY" "$DEMO_CASE_OUTPUT" --scene)
if [ "$DEMO_BEFORE" = yes ]; then demo_args+=(--before); fi
exec "$DEMO_PYTHON" "$DEMO_DRIVER" "${demo_args[@]}"
SCENE
{
  echo 'feature: quoted image prompt with spinner, elapsed time, and plain saving status'
  echo "before source commit: $demo_before_sha"
  echo "after source commit/base: $demo_after_sha"
  echo "after source detail: ${DEMO_AFTER_SOURCE_DETAIL:-clean commit provided above}"
  echo 'command: openai images generate'
  echo 'input: A synthetic orange robot; Enter; append with a blue hat; Enter; Ctrl+C'
  echo 'responses: HTTP400 first; synthetic one-pixel PNG second; each delayed four seconds'
  echo 'security: loopback server; fake key; isolated HOME; no shell startup hooks'
  echo 'capture: actual native macOS zsh PTY inside asciinema'
  echo 'render: terminal text replay through agg; native image graphics remain unverified'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_driver" "$demo_source/image_recovery_check.py" "$demo_source/image_friendly_loading_record.sh"
} > "$demo_output/metadata.txt"
for demo_scene in before after; do
  demo_binary="$demo_after"
  demo_is_before=no
  if [ "$demo_scene" = before ]; then demo_binary="$demo_before"; demo_is_before=yes; fi
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/after" 'http://127.0.0.1' "$demo_scene" \
    PYTHONDONTWRITEBYTECODE=1 DEMO_PYTHON="$demo_python" DEMO_DRIVER="$demo_driver" \
    DEMO_BINARY="$demo_binary" DEMO_CASE_OUTPUT="$demo_output/$demo_scene-evidence" DEMO_BEFORE="$demo_is_before"
  "$demo_python" - "$demo_output/$demo_scene.cast" "$demo_scene" <<'FRAMES' > "$demo_output/$demo_scene-times.txt"
import json, pathlib, sys
events = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines()[1:]]
text, waiting, draft, completed, saved = "", None, None, None, None
for event in events:
    if event[1] != "o":
        continue
    text += event[2]
    if waiting is None and "Generating image" in text:
        waiting = event[0] + 0.5
    if draft is None and any(note in text for note in ("Your draft is kept", "Your prompt and settings are still here")):
        draft = event[0] + 0.2
    if saved is None and "All images saved" in text:
        saved = event[0] + 0.2
    if completed is None and "Images saved" in text:
        completed = event[0] + 0.2
assert waiting is not None and draft is not None and saved is not None, "Expected loading, recovery, and saved frames are missing"
if sys.argv[2] == "after":
    assert completed is not None, "The candidate completion frame is missing"
print(f"waiting {waiting}")
print(f"waiting-next {waiting + 2.0}")
print(f"draft {draft}")
if completed is not None:
    print(f"completed {completed}")
print(f"saved {saved}")
FRAMES
  while read -r demo_phase demo_frame_time; do
    "$demo_agg" --quiet "${demo_render_options[@]}" --select "$demo_frame_time" \
      "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-$demo_phase.gif"
    "$demo_ffmpeg" -nostdin -hide_banner -loglevel error -y -i "$demo_output/$demo_scene-$demo_phase.gif" \
      -frames:v 1 "$demo_output/$demo_scene-$demo_phase.png"
  done < "$demo_output/$demo_scene-times.txt"
done
"$demo_python" - "$demo_output" <<'REQUESTS'
import json, pathlib, sys
root = pathlib.Path(sys.argv[1])
before = json.loads((root / 'before-evidence/results.json').read_text())['cases'][0]
after = json.loads((root / 'after-evidence/results.json').read_text())['cases'][0]
assert before['result'] == after['result'] == 'pass'
assert len(before['requests']) == len(after['requests']) == 2
assert before['requests'] == after['requests'], 'feedback changed request bodies or request count'
comparison = dict(requests_equal=True, before_count=2, after_count=2,
                  note='Identical synthetic failure and deliberate retry payloads; no added requests or preview settings.')
(root / 'request-comparison.json').write_text(json.dumps(comparison, indent=2) + '\n')
REQUESTS
demo_assemble_capture 200 before after
printf 'Recorded friendly image recovery in %s\n' "$demo_output"
