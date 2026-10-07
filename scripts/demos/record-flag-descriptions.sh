#!/bin/bash
# Record complete help in 90x76 and 56x76 PTYs. Keep all media outside Git.
# Preview the first 70 terminal lines; preserve complete PTY output separately.
# Build the existing fixture before running:
#   go build -o /tmp/image-model-demo-api ./scripts/demos/image-models
#   DEMO_API_BINARY=/tmp/image-model-demo-api scripts/demos/record-flag-descriptions.sh \
#     BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-flag-descriptions.sh BEFORE_BINARY AFTER_BINARY BASE_SHA CANDIDATE_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_python="$(command -v python3)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_output_root="$demo_output"
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 3)
demo_start_api "$demo_runtime/requests.txt"
{
  echo 'feature: one complete help page with readable flag descriptions'
  echo "base commit: $demo_before_sha"
  echo "candidate commit: $demo_after_sha"
  echo 'data: no API key; loopback fixture records unexpected requests'
  echo 'capture: real binaries in isolated bash PTYs; no personal environment or shell hooks'
  echo 'geometry: actual stdout PTY checked with stty; 90x76 and 56x76'
  echo 'preview: labeled first 70 terminal lines, derived from complete PTY output'
  echo 'full evidence: *-full.cast, *-full.txt, *-stdout.bin, *-stdout.txt, *-stderr.txt'
  echo 'stdout.bin retains UTF-8 PTY bytes; stdout.txt normalizes only CRLF endings'
  echo 'scope: terminal replay; no native graphical terminal claim'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-flag-descriptions.sh" "$demo_source/image-models/main.go"
} > "$demo_output_root/metadata.txt"

# Keep original recordings untouched. Excerpts contain only plain help text.
cat > "$demo_runtime/excerpt.py" <<'PYTHON'
import json
from pathlib import Path
import sys
import unicodedata

directory, scene, columns, topic, *arguments = sys.argv[1:]
directory = Path(directory)
columns = int(columns)
records = [json.loads(line) for line in (directory / f"{scene}-full.cast").read_text().splitlines()]
header, events = records[0], records[1:]
if (header["width"], header["height"]) != (columns, 76):
    raise SystemExit("The full capture has unexpected PTY dimensions.")
if (directory / f"{scene}-geometry.txt").read_text().split() != ["76", str(columns)]:
    raise SystemExit("The command's actual stdout PTY dimensions did not match.")
if any(event[1] == "r" for event in events):
    raise SystemExit("The terminal resized during capture.")
recorded = "".join(event[2] for event in events if event[1] == "o")
command = "$ openai " + " ".join(arguments)
_, found, output = recorded.partition(command + "\r\n")
suffix = "\r\n$ "
if not found or not output.endswith(suffix):
    raise SystemExit("The capture lacks complete command-output boundaries.")
output = output[:-len(suffix)]
if not output or (directory / f"{scene}-stderr.txt").read_bytes():
    raise SystemExit("Help must produce stdout with empty stderr.")
if any(value in output for value in ("synthetic-demo-key", "/help-only/v1")):
    raise SystemExit("Help disclosed a configured fixture value.")
(directory / f"{scene}-stdout.bin").write_bytes(output.encode("utf-8"))
plain = output.replace("\r\n", "\n")
(directory / f"{scene}-stdout.txt").write_bytes(plain.encode("utf-8"))

# Count terminal rows, including soft wraps in long synopsis/example lines.
# Reject controls instead of silently changing the recorded command output.
rows = []
lines = plain.split("\n")[:-1] if plain.endswith("\n") else plain.split("\n")
for line in lines:
    current, cells = "", 0
    for character in line.expandtabs(8):
        if unicodedata.category(character).startswith("C"):
            raise SystemExit("Unsupported control character in plain help output.")
        width = 0 if unicodedata.combining(character) else 2 if unicodedata.east_asian_width(character) in "WF" else 1
        if cells + width > columns:
            rows.append(current)
            current, cells = "", 0
        current += character
        cells += width
    rows.append(current)

label = f"{scene.title()}: {topic} ({columns} columns)"
caption = "Excerpt: first 70 terminal lines"
excerpt = "\n".join(rows[:70])
text = f"{label}\n{caption}\n\n{command}\n{excerpt}\n[Full stdout retained separately]\n"
header.update(title=f"{label}; {caption}", width=columns, height=76)
with (directory / f"{scene}.cast").open("w") as stream:
    stream.write(json.dumps(header) + "\n")
    stream.write(json.dumps([0.0, "o", "\u001b[2J\u001b[H"]) + "\n")
    stream.write(json.dumps([0.3, "o", text.replace("\n", "\r\n")]) + "\n")
(directory / f"{scene}.txt").write_text(text)
with (directory / "metadata.txt").open("a") as stream:
    stream.write(f"{scene}: {len(rows)} complete terminal lines; excerpt shows {min(len(rows), 70)}\n")
PYTHON

for demo_topic in root images-generate; do
  demo_expected_status=0
  case "$demo_topic" in
    root) demo_args=(--help);;
    images-generate) demo_args=(images generate --help);;
  esac
  {
    cat <<'SCENE'
#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
demo_geometry="$(stty size)" || exit 98
test "$demo_geometry" = "$LINES $COLUMNS" || exit 98
printf '%s\n' "$demo_geometry" > "$DEMO_GEOMETRY_FILE"
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
# Keep stdout on the real PTY. Redirected stdout would force 80-column help.
if openai "${demo_args[@]}" 2>"$DEMO_STDERR_FILE"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
  } > "$demo_runtime/scene.sh"

  for demo_columns in 90 56; do
    demo_window_size="${demo_columns}x76"
    demo_output="$demo_output_root/$demo_topic-$demo_columns"
    mkdir "$demo_output"
    {
      printf 'command: openai'; printf ' %s' "${demo_args[@]}"; printf '\n'
      echo "render: asciinema + agg, Menlo 18px, Dracula, $demo_window_size, line height 1.2"
      echo 'before/after media: first-70-terminal-line excerpts, not complete pages'
      echo 'before-full/after-full: untouched complete PTY captures and transcripts'
    } > "$demo_output/metadata.txt"
    cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
    for demo_scene in before after; do
      demo_capture_scene "$demo_scene-full" "$demo_expected_status" "$demo_runtime/$demo_scene" "$demo_api_url/help-only/v1" \
        "$demo_scene: $demo_topic ($demo_columns columns; complete capture)" \
        "COLUMNS=$demo_columns" "LINES=76" \
        "DEMO_GEOMETRY_FILE=$demo_output/$demo_scene-geometry.txt" \
        "DEMO_STDERR_FILE=$demo_output/$demo_scene-stderr.txt"
      test ! -s "$demo_output/$demo_scene-stderr.txt"
      test ! -s "$demo_runtime/requests.txt"
      "$demo_python" "$demo_runtime/excerpt.py" "$demo_output" "$demo_scene" "$demo_columns" "$demo_topic" "${demo_args[@]}"
      "$demo_agg" --quiet "${demo_render_options[@]}" "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene.gif"
      "$demo_agg" --quiet "${demo_render_options[@]}" --select 100% \
        "$demo_output/$demo_scene.cast" "$demo_output/$demo_scene-frame.gif"
      "$demo_ffmpeg" -hide_banner -loglevel error -y \
        -i "$demo_output/$demo_scene-frame.gif" -frames:v 1 "$demo_output/$demo_scene.png"
    done
    test ! -s "$demo_runtime/requests.txt"
    demo_assemble_capture 300 before after
  done
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output_root/requests.txt"
test ! -s "$demo_output_root/requests.txt"
echo 'API requests: 0 (required)' >> "$demo_output_root/metadata.txt"
printf 'Recorded complete help and labeled excerpts in %s\n' "$demo_output_root"
