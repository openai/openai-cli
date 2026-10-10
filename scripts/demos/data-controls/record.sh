#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_go="$(command -v go)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
"$demo_python" -I -B - "$demo_go" "$demo_before" "$demo_before_sha" "$demo_after" "$demo_after_sha" "$demo_output" <<'PY'
import os, pathlib, subprocess, sys
go, before, before_sha, after, after_sha, directory = sys.argv[1:]
output = pathlib.Path(directory)
for label, binary, expected in (("before", before, before_sha), ("after", after, after_sha)):
    result = subprocess.run([go, "version", "-m", binary], capture_output=True, text=True, timeout=15, check=True)
    (output / (label + "-build.txt")).write_text(result.stdout)
    settings = {}
    for line in result.stdout.splitlines():
        fields = line.strip().split("\t", 1)
        if len(fields) == 2 and fields[0] == "build" and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            settings[key] = value
    if settings.get("vcs.revision") != expected:
        raise SystemExit(label + " binary lacks the supplied source revision; rebuild with VCS metadata")
    if settings.get("vcs.modified") != "false":
        if label != "after" or not os.environ.get("DEMO_AFTER_SOURCE_STATE"):
            raise SystemExit(label + " binary has dirty or unknown source state; supply an explicit candidate manifest")
        print("Candidate binary has dirty source; DEMO_AFTER_SOURCE_STATE identifies the reviewed manifest.", file=sys.stderr)
PY
demo_before_digest="$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
demo_after_digest="$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
mkdir "$demo_runtime/home"
demo_start_api "$demo_output/requests.jsonl" "$demo_output/fixture.json"

cat > "$demo_runtime/scene.sh" <<'SCENE'
#!/bin/bash
set -euo pipefail
exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER"
SCENE

{
  echo 'feature: configured retention and external-storage validation states'
  echo "before source commit supplied by caller: $demo_before_sha"
  echo "after source commit supplied by caller: $demo_after_sha"
  echo "after source state: ${DEMO_AFTER_SOURCE_STATE:-committed source supplied by caller}"
  echo 'source checks: go version -m revision matches supplied SHA; build metadata retained'
  echo 'data: loopback synthetic responses only; no cloud or retention changes'
  echo 'pending validation requests ext_requested; the response ID is ext_returned'
  echo 'validated retrieval uses ext_returned; it does not perform validation'
  echo 'every scene captures default output and separately verifies explicit JSON'
  echo 'capture: real CLI processes in a PTY; each command has a 15-second deadline'
  echo 'settings: isolated HOME, synthetic Admin key, NO_COLOR=1, FORCE_COLOR=0, PAGER=cat'
  echo 'render: agg swash, Menlo 18px, Dracula, 120 columns x 32 rows'
  echo 'scope: terminal replay; no native terminal appearance or real-provider acceptance claim'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/server.py" "$demo_source/scene.py" \
  "$demo_source/validate.py" "$demo_root/scripts/demos/capture_and_render.sh" \
  > "$demo_output/source-sha256.txt"
demo_window_size=120x32
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme dracula --fps-cap 20 --last-frame-duration 2)
demo_scenes=()
for demo_case in retention pending validated; do
  for demo_mode in before after; do
    demo_scene="$demo_mode-$demo_case"
    demo_scenes+=("$demo_scene")
    demo_capture_scene "$demo_scene" 0 "$demo_runtime/$demo_mode" \
      "$demo_api_url/$demo_mode/$demo_case" "$demo_mode: $demo_case" \
      "HOME=$demo_runtime/home" "OPENAI_ADMIN_KEY=synthetic-admin-key" \
      'NO_COLOR=1' 'FORCE_COLOR=0' 'PAGER=cat' 'GOMAXPROCS=2' \
      "DEMO_PYTHON=$demo_python" "DEMO_DRIVER=$demo_source/scene.py" \
      "DEMO_MODE=$demo_mode" "DEMO_CASE=$demo_case" "DEMO_OUTPUT=$demo_output"
  done
done
demo_stop_api
test "$demo_before_digest" = "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)"
test "$demo_after_digest" = "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)"
shasum -a 256 -c "$demo_output/source-sha256.txt" > "$demo_output/source-validation.txt"
"$demo_python" -I -B "$demo_source/validate.py" "$demo_output" | tee "$demo_output/validation.txt"
demo_assemble_capture 200 "${demo_scenes[@]}"
shasum -a 256 "$demo_output/"*.png "$demo_output/comparison.gif" > "$demo_output/media-sha256.txt"
printf 'Recorded data-controls comparison in %s\n' "$demo_output"
