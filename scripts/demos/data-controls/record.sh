#!/bin/bash
set -euo pipefail

if [ "$#" -ne 5 ]; then
  echo 'usage: record.sh BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
demo_window_size="${DEMO_WINDOW_SIZE:-120x32}"
case "$demo_window_size" in
  120x32|80x40|40x60) ;;
  *) echo 'DEMO_WINDOW_SIZE must be 120x32, 80x40, or 40x60.' >&2; exit 2;;
esac
demo_theme="${DEMO_THEME:-dracula}"
case "$demo_theme" in
  dracula|github-light) ;;
  *) echo 'DEMO_THEME must be dracula or github-light.' >&2; exit 2;;
esac
demo_source="$(cd "$(dirname "$0")" && pwd)"
demo_root="$(cd "$demo_source/../../.." && pwd)"
demo_python="$(command -v python3)"
demo_go="$(command -v go)"
source "$demo_source/../capture_and_render.sh"
demo_prepare_capture "$demo_root" "$1" "$2" "$3" "$4" "$5" "$demo_source/server.py"
"$demo_python" -I -B - "$demo_go" "$demo_before" "$demo_before_sha" "$demo_after" "$demo_after_sha" "$demo_output" <<'PY'
import hashlib, json, os, pathlib, subprocess, sys
go, before, before_sha, after, after_sha, directory = sys.argv[1:]
output = pathlib.Path(directory)

def unique_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate build manifest key")
        result[key] = value
    return result

for label, binary, expected in (("before", before, before_sha), ("after", after, after_sha)):
    result = subprocess.run([go, "version", "-m", binary], capture_output=True, text=True, timeout=15, check=True)
    (output / (label + "-build.txt")).write_text(result.stdout)
    settings = {}
    for line in result.stdout.splitlines():
        fields = line.strip().split("\t", 1)
        if len(fields) == 2 and fields[0] == "build" and "=" in fields[1]:
            key, value = fields[1].split("=", 1)
            settings[key] = value
    manifest_path = os.environ.get("DEMO_BEFORE_BUILD_MANIFEST" if label == "before" else "DEMO_AFTER_BUILD_MANIFEST")
    if manifest_path:
        manifest_bytes = pathlib.Path(manifest_path).read_bytes()
        manifest = json.loads(manifest_bytes, object_pairs_hook=unique_object)
        if not isinstance(manifest, dict):
            raise SystemExit(label + " build manifest must be an object")
        if any(key == "vcs" or key.startswith("vcs.") for key in settings):
            if settings.get("vcs.revision") != expected or settings.get("vcs.modified") != "false":
                raise SystemExit(label + " build manifest contradicts embedded VCS metadata")
        if label == "after":
            for phase in ("source_before", "source_after"):
                state = manifest.get(phase)
                if not isinstance(state, dict) or state.get("revision") != expected or state.get("clean") is not True:
                    raise SystemExit("candidate manifest must record the same clean source before and after the build")
            command = manifest.get("build_command")
            if not isinstance(command, list) or not command or any(not isinstance(arg, str) or not arg for arg in command):
                raise SystemExit("candidate manifest must record the successful build command")
        if manifest.get("source_sha") != expected:
            raise SystemExit(label + " build manifest source_sha does not match the supplied source revision")
        recorded_binary = pathlib.Path(manifest.get("binary_path", "")).resolve(strict=True)
        if recorded_binary != pathlib.Path(binary).resolve(strict=True):
            raise SystemExit(label + " build manifest binary_path does not match the supplied binary")
        digest = hashlib.sha256()
        with open(binary, "rb") as binary_file:
            for chunk in iter(lambda: binary_file.read(1024 * 1024), b""):
                digest.update(chunk)
        if manifest.get("binary_sha256") != digest.hexdigest():
            raise SystemExit(label + " binary does not match its build manifest hash")
        if type(manifest.get("exit_code")) is not int or manifest["exit_code"] != 0 or manifest.get("result") != "pass":
            raise SystemExit(label + " build manifest does not report a successful build")
        (output / (label + "-build-manifest.json")).write_bytes(manifest_bytes)
        continue
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
  if [ -n "${DEMO_BEFORE_BUILD_MANIFEST:-}" ]; then
    echo 'before source: caller-supplied archive-build manifest; source SHA, binary path/hash, and successful build result verified'
    echo 'before provenance: copied before-build-manifest.json retains archive hash and build commands; no baseline VCS metadata claim'
  else
    echo 'before source: go version -m revision matches supplied SHA and source is clean'
  fi
  if [ -n "${DEMO_AFTER_BUILD_MANIFEST:-}" ]; then
    echo 'after source: reviewed build manifest; matching clean before/after source, build command/result, and binary path/hash verified'
    echo 'after provenance: copied after-build-manifest.json; embedded VCS metadata is not required when absent'
  else
    echo 'after source: go version -m revision matches supplied SHA; build metadata retained'
  fi
  echo 'data: loopback synthetic responses only; no cloud or retention changes'
  echo 'pending validation requests ext_requested; the response ID is ext_returned'
  echo 'validated retrieval uses ext_returned; it does not perform validation'
  echo 'every scene captures default output and separately verifies explicit JSON'
  echo 'capture: real CLI processes in a PTY; each command has a 15-second deadline'
  echo 'settings: isolated HOME, synthetic Admin key, NO_COLOR=1, FORCE_COLOR=0, PAGER=cat'
  echo "render: agg swash, Menlo 18px, theme $demo_theme, window $demo_window_size"
  echo 'scope: terminal replay; no native terminal appearance or real-provider acceptance claim'
  demo_capture_metadata
  "$demo_python" --version
} > "$demo_output/metadata.txt"
shasum -a 256 "$demo_source/record.sh" "$demo_source/server.py" "$demo_source/scene.py" \
  "$demo_source/validate.py" "$demo_root/scripts/demos/capture_and_render.sh" \
  > "$demo_output/source-sha256.txt"
demo_render_options=(--renderer swash --font-family Menlo --font-size 18 --line-height 1.2 \
  --theme "$demo_theme" --fps-cap 20 --last-frame-duration 2)
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
