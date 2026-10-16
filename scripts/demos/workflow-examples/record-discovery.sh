#!/bin/bash
set -euo pipefail
if [ "$#" -ne 6 ] || [ "$1" != --run-authorized-pty-slot ]; then
  echo 'usage: record-discovery.sh --run-authorized-pty-slot BEFORE_BINARY AFTER_BINARY BEFORE_RUNTIME_SHA AFTER_SHA OUTPUT_DIR' >&2
  exit 2
fi
shift
: "${DEMO_BEFORE_SHA256:?Set the verified prior draft binary SHA256}"
: "${DEMO_AFTER_SHA256:?Set the verified candidate binary SHA256}"
: "${DEMO_SOURCE_MANIFEST:?Set the source and binary provenance manifest path}"
test -f "$DEMO_SOURCE_MANIFEST"
record_source="$(cd "$(dirname "$0")" && pwd -P)"
record_repo="$(cd "$record_source/../../.." && pwd -P)"
source "$record_repo/scripts/demos/capture_and_render.sh"
# The shared lifecycle requires an executable fixture path; no fixture starts.
demo_prepare_capture "$record_repo" "$1" "$2" "$3" "$4" "$5" /usr/bin/true
discovery_output="$demo_output"
cp "$record_source/discovery-scene.sh" "$demo_runtime/scene.sh"
cp "$DEMO_SOURCE_MANIFEST" "$discovery_output/source-manifest.txt"
cp "$record_source/record-discovery.sh" "$record_source/discovery-scene.sh" \
  "$record_source/validate-discovery.py" "$discovery_output/"
discovery_verify_binaries() {
  test "$(shasum -a 256 "$demo_before" | cut -d ' ' -f 1)" = "$DEMO_BEFORE_SHA256"
  test "$(shasum -a 256 "$demo_after" | cut -d ' ' -f 1)" = "$DEMO_AFTER_SHA256"
}
discovery_verify_binaries
{
  echo 'feature: compact workflow discovery and copied local recovery'
  echo 'comparison: prior published draft versus follow-up candidate'
  echo 'prior published draft: e0e2c883800bae202151061778be6c598d0a1341'
  echo "before binary runtime source: $demo_before_sha"
  echo "after binary source: $demo_after_sha"
  echo 'original main comparison: e68939820415144d769ed02de6aa72d5b7d32948; separate original recipe demo'
  echo 'capture: isolated Bash PTYs; no credentials; invalid remote configuration; no API fixture'
  echo 'scope: terminal replay; no native graphical terminal, font, or live API validation'
  echo 'baseline: complete bare output; natural scrolling in the actual 24-row viewport'
  echo 'recovery: copied diagnostic command prints a recipe; no API recipe executes'
  echo 'profiles: dark 80x24; light 80x24; NO_COLOR 40x24; Menlo 18px'
  demo_capture_metadata
  shasum -a 256 "$record_source/record-discovery.sh" "$record_source/discovery-scene.sh" \
    "$record_source/validate-discovery.py" "$discovery_output/source-manifest.txt"
} > "$discovery_output/metadata.txt"
for demo_role in before after; do
  mkdir -p "$demo_runtime/$demo_role-probe-home"
  for demo_probe in index failure files files-json; do
    case "$demo_probe" in
      index) demo_args=(examples); demo_expected=0;;
      failure) demo_args=(examples files --format yaml --format-error text); demo_expected=1;;
      files) demo_args=(examples files); demo_expected=0;;
      files-json) demo_args=(examples files --format json); demo_expected=0;;
    esac
    if env -i PATH="$demo_runtime/$demo_role:/usr/bin:/bin" LANG=en_US.UTF-8 \
      HOME="$demo_runtime/$demo_role-probe-home" TERM=dumb NO_COLOR=1 GOMAXPROCS=2 \
      OPENAI_BASE_URL='://offline-discovery-demo' OPENAI_MTLS_CLIENT_CERT_FILE=/missing-demo-cert \
      OPENAI_MTLS_CLIENT_KEY_FILE=/missing-demo-key openai "${demo_args[@]}" \
      > "$discovery_output/$demo_role-$demo_probe.stdout" 2> "$discovery_output/$demo_role-$demo_probe.stderr"; \
      then demo_status=0; else demo_status=$?; fi
    printf '%s %s probe status: %s (expected %s)\n' "$demo_role" "$demo_probe" "$demo_status" "$demo_expected" \
      >> "$discovery_output/metadata.txt"
    test "$demo_status" -eq "$demo_expected"
  done
done
python3 "$record_source/validate-discovery.py" prepare "$discovery_output"
for demo_profile in dark light no-color; do
  case "$demo_profile" in
    dark) demo_window_size=80x24; demo_palette=asciinema; demo_theme_environment=('COLORFGBG=15;0' COLORTERM=truecolor);;
    light) demo_window_size=80x24; demo_palette=github-light; demo_theme_environment=('COLORFGBG=0;15' COLORTERM=truecolor);;
    no-color) demo_window_size=40x24; demo_palette=asciinema; demo_theme_environment=(NO_COLOR=1 FORCE_COLOR=0 'COLORFGBG=15;0');;
  esac
  demo_render_options=(--renderer resvg --font-family Menlo --font-size 18 --line-height 1.2 \
    --theme "$demo_palette" --fps-cap 15 --last-frame-duration 3)
  demo_output="$discovery_output/$demo_profile"
  mkdir "$demo_output"
  cp "$discovery_output/metadata.txt" "$demo_output/metadata.txt"
  printf 'profile: %s; viewport: %s; replay palette: %s\n' "$demo_profile" "$demo_window_size" "$demo_palette" \
    >> "$demo_output/metadata.txt"
  for demo_mode in index recovery; do
    for demo_role in before after; do
      demo_scene="$demo_role"
      if [ "$demo_mode" = recovery ]; then demo_scene="$demo_role-recovery"; fi
      demo_expected=0
      if [ "$demo_mode/$demo_role" = recovery/before ]; then demo_expected=1; fi
      demo_role_label=BEFORE
      if [ "$demo_role" = after ]; then demo_role_label=AFTER; fi
      mkdir -p "$demo_runtime/$demo_profile-$demo_role-home"
      demo_registration=""
      if [ -n "${DEMO_CAPTURE_REGISTRY:-}" ]; then
        # Record the launch intent before asciinema can create a separate PTY group.
        test ! -e "$DEMO_CAPTURE_REGISTRY/closed"
        demo_registration="$DEMO_CAPTURE_REGISTRY/$demo_profile-$demo_scene"
        mkdir "$demo_registration"
      fi
      demo_capture_scene "$demo_scene" "$demo_expected" "$demo_runtime/$demo_role" '://offline-discovery-demo' \
        "$demo_role_label: $demo_mode ($demo_profile)" "HOME=$demo_runtime/$demo_profile-$demo_role-home" \
        "DEMO_DISCOVERY_MODE=$demo_mode" "DEMO_ROLE=$demo_role" "DEMO_ROLE_LABEL=$demo_role_label" \
        "DEMO_STATUS_FILE=$demo_output/$demo_scene.status" "DEMO_RECOVERY_FILE=$discovery_output/recovery-command.txt" \
        GOMAXPROCS=2 OPENAI_MTLS_CLIENT_CERT_FILE=/missing-demo-cert OPENAI_MTLS_CLIENT_KEY_FILE=/missing-demo-key \
        "${demo_theme_environment[@]}" "DEMO_CAPTURE_REGISTRATION=$demo_registration"
    done
  done
  demo_assemble_capture 300 before after before-recovery after-recovery
done
demo_output="$discovery_output"
python3 "$record_source/validate-discovery.py" check "$discovery_output" "$demo_runtime"
discovery_verify_binaries
printf 'Recorded workflow discovery in %s\n' "$discovery_output"
