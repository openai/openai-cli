#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record-welcome.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../.." && pwd)"
demo_columns="${DEMO_COLUMNS:-90}"
demo_rows="${DEMO_ROWS:-250}"
demo_preview_rows="${DEMO_PREVIEW_ROWS:-32}"
for demo_number in "$demo_columns" "$demo_rows" "$demo_preview_rows"; do
  [[ "$demo_number" =~ ^[1-9][0-9]{0,2}$ ]] || {
    echo 'DEMO_COLUMNS, DEMO_ROWS, and DEMO_PREVIEW_ROWS must be integers from 1 to 999.' >&2
    exit 2
  }
done
if [ "$demo_preview_rows" -gt "$demo_rows" ]; then
  echo 'DEMO_PREVIEW_ROWS cannot exceed DEMO_ROWS.' >&2
  exit 2
fi
demo_no_color="${NO_COLOR:-}"
demo_theme="${DEMO_THEME:-dark}"
case "$demo_theme" in
  dark) demo_palette=dracula; demo_default_colorfgbg='15;0';;
  light) demo_palette=github-light; demo_default_colorfgbg='0;15';;
  *) echo 'DEMO_THEME must be dark or light.' >&2; exit 2;;
esac
demo_colorfgbg="${COLORFGBG:-$demo_default_colorfgbg}"
demo_colorterm="${COLORTERM:-truecolor}"
demo_python="$(command -v python3)"
source "$demo_source/capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" \
  "${DEMO_API_BINARY:-$demo_root/dist/demos/bin/image-model-demo-api}"
demo_window_size="${demo_columns}x${demo_rows}"
demo_render_options=(--font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "$demo_palette" --fps-cap 20 --last-frame-duration 3)
demo_start_api "$demo_runtime/requests.txt"
cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_WEBHOOK_SECRET
cd "$DEMO_WORKING_DIRECTORY"
openai --version > "$DEMO_VERSION_FILE"
printf '\033[2J\033[H%s\n\n' "$DEMO_SCENE_LABEL"
sleep 0.4
printf '$ openai\n'
sleep 0.3
if openai; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
SCENE
{
  echo 'feature: welcome banner on bare openai'
  echo "before commit: $demo_before_sha"
  echo "after commit: $demo_after_sha"
  echo 'command: openai'
  echo 'version check: openai --version, saved separately before each scene'
  echo 'data: no API credentials; rejecting loopback fixture records unexpected requests'
  echo 'capture: isolated Bash PTYs, temporary HOME and XDG directories, restricted PATH, no shell hooks'
  echo 'startup: existing first-run behavior remains enabled within temporary directories'
  echo "render: asciinema + agg, Menlo 18px, $demo_palette, $demo_window_size, line height 1.2"
  echo "application color environment: COLORFGBG=$demo_colorfgbg COLORTERM=$demo_colorterm TERM=xterm-256color"
  if [ -n "$demo_no_color" ]; then echo 'NO_COLOR: enabled'; else echo 'NO_COLOR: unset'; fi
  echo "top crops: first approximately $demo_preview_rows rows; full media and transcripts remain available"
  echo 'scope: terminal replay; not native graphical terminal, Linux, Windows, or font validation'
  demo_capture_metadata
  "$demo_python" --version
  shasum -a 256 "$demo_source/record-welcome.sh" "$demo_source/welcome/README.md" \
    "$demo_source/image-models/main.go"
} > "$demo_output/metadata.txt"
cp "$demo_runtime/scene.sh" "$demo_output/scene.sh"
for demo_scene in before after; do
  demo_state="$demo_runtime/$demo_scene-state"
  mkdir -p "$demo_state/home" "$demo_state/config" "$demo_state/cache" \
    "$demo_state/data" "$demo_state/work"
  demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_scene" \
    "$demo_api_url/help-only/v1" "$demo_scene: bare openai" \
    "HOME=$demo_state/home" "XDG_CONFIG_HOME=$demo_state/config" \
    "XDG_CACHE_HOME=$demo_state/cache" "XDG_DATA_HOME=$demo_state/data" \
    "DEMO_WORKING_DIRECTORY=$demo_state/work" "DEMO_VERSION_FILE=$demo_output/$demo_scene-version.txt" \
    "NO_COLOR=$demo_no_color" "COLORFGBG=$demo_colorfgbg" "COLORTERM=$demo_colorterm" "GOMAXPROCS=2"
done
demo_stop_api
cp "$demo_runtime/requests.txt" "$demo_output/requests.txt"
test ! -s "$demo_output/requests.txt"
"$demo_python" -I -B - "$demo_output" "$demo_columns" "$demo_rows" "$demo_no_color" <<'PY'
import json
import math
import pathlib
import re
import sys
import unicodedata

output = pathlib.Path(sys.argv[1])
columns, rows = map(int, sys.argv[2:4])
no_color = bool(sys.argv[4])
csi = re.compile(r"\x1b\[[0-?]*[ -/]*[@-~]")

def cell_width(text):
    return sum(0 if unicodedata.combining(char) else
               2 if unicodedata.east_asian_width(char) in ("W", "F") else 1
               for char in text)

def read_scene(name):
    events = [json.loads(line) for line in (output / f"{name}.cast").read_text().splitlines()]
    assert (events[0]["width"], events[0]["height"]) == (columns, rows), "unexpected capture dimensions"
    recorded = "".join(event[2] for event in events[1:] if event[1] == "o")
    marker, prompt = "$ openai\r\n", "\r\n$ "
    assert recorded.count(marker) == 1 and recorded.endswith(prompt), "ambiguous command boundaries"
    cli_output = recorded.split(marker, 1)[1][:-len(prompt)]
    plain = csi.sub("", cli_output).replace("\r\n", "\n")
    assert "\x1b" not in plain, "unexpected terminal control sequence"
    full_scene = csi.sub("", recorded).replace("\r\n", "\n")
    occupied = sum(max(1, math.ceil(cell_width(line) / columns)) for line in full_scene.split("\n"))
    assert occupied < rows, f"{name} needs more than {occupied} rows; raise DEMO_ROWS"
    assert plain.startswith("NAME:\n") or plain.startswith("┌"), "missing help start"
    for expected in ("START HERE\n", "GLOBAL OPTIONS:\n", "Key setup: openai help setup\n"):
        assert expected in plain, f"{name} missing {expected!r}"
    if no_color:
        assert "\x1b" not in cli_output, f"{name} ignored NO_COLOR"
    (output / f"{name}-cli.txt").write_text(plain)
    (output / f"{name}-cli.tty").write_bytes(cli_output.encode())
    return cli_output, plain

before_raw, before = read_scene("before")
after_raw, after = read_scene("after")
report = (output / "after-version.txt").read_text().strip()
assert report.startswith("openai version "), "unexpected runtime version report"
version = report.removeprefix("openai version ")
assert version, "runtime version is empty"
display_version = "v" + version if version[0].isdigit() else version
title = "OpenAI CLI  " + display_version
greeting = "What are we making today?"
banner_width = max(cell_width(title), cell_width(greeting)) + 4
assert greeting not in before, "baseline already contains the welcome banner"
if banner_width <= min(columns, 100):
    banner, help_text = after.split("\n\n", 1)
    lines = banner.splitlines()
    expected_height = 6 if columns >= 40 else 4
    title_row = 2 if columns >= 40 else 1
    assert len(lines) == expected_height and lines[0].startswith("┌") and lines[-1].startswith("└"), "invalid banner frame"
    assert "OpenAI CLI" in lines[title_row] and display_version in lines[title_row], "title/version row wrapped"
    assert greeting in lines[title_row + 1], "missing greeting"
    assert "✦" not in banner and ">_" not in banner, "unexpected signature mark"
    assert all(cell_width(line) <= columns for line in lines), "banner exceeds terminal width"
    assert help_text == before, "welcome changed existing help or command groups"
    assert after_raw.split("\r\n\r\n", 1)[1] == before_raw, "welcome changed existing help bytes"
else:
    assert after_raw == before_raw, "narrow fallback changed existing help bytes"
(output / "validation.txt").write_text(
    "PASS: both public commands exit zero; full help fits the capture viewport.\n"
    "PASS: runtime version matches; banner fits or narrow fallback preserves exact baseline help.\n"
    "PASS: existing help and command groups remain byte-identical after removing the banner.\n"
    "PASS: API fixture received zero requests.\n" +
    ("PASS: NO_COLOR output contains no terminal escapes.\n" if no_color else ""))
PY
echo 'API requests: 0 (required)' >> "$demo_output/metadata.txt"
demo_assemble_capture 300 before after
# These crops supplement the full captures. They never replace evidence files.
demo_crop="crop=iw:floor(ih*$demo_preview_rows/$demo_rows):0:0"
for demo_scene in before after; do
  # Select the complete replay's final frame. A separate percentage-based
  # still render can capture an early frame when output arrives in a burst.
  demo_frame_count="$("$demo_ffprobe" -v error -select_streams v:0 \
    -show_entries stream=nb_frames -of csv=p=0 "$demo_output/$demo_scene.gif")"
  [[ "$demo_frame_count" =~ ^[1-9][0-9]*$ ]]
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene.gif" \
    -vf "select=eq(n\\,$((demo_frame_count-1)))" -frames:v 1 "$demo_output/$demo_scene.png"
  "$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/$demo_scene.png" \
    -vf "$demo_crop" -frames:v 1 "$demo_output/$demo_scene-top.png"
done
"$demo_ffmpeg" -hide_banner -loglevel error -y -i "$demo_output/comparison.gif" \
  -filter_complex "$demo_crop,split[a][b];[a]palettegen[p];[b][p]paletteuse" \
  -vsync 0 -gifflags 0 -loop 0 -final_delay 300 "$demo_output/comparison-top.gif"
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" \
  "$demo_output/comparison-top.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded welcome comparison in %s\n' "$demo_output"
