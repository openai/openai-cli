#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Terminal replay | synthetic loopback API | fake credentials'
printf '%s\n' '$ openai --version'
openai --version
printf 'Source commit: %s\n\n' "$DEMO_SOURCE_SHA"
sleep 0.3
capture_flags=(--debug)
capture_stream=false
case "$DEMO_CAPTURE_KIND" in
  before-body) ;;
  after-body) capture_flags+=(--debug-body);;
  before-stream) capture_stream=true;;
  after-stream|after-limits)
    capture_stream=true
    capture_flags+=(--debug-body --debug-sse --debug-event-timing);;
  *) exit 2;;
esac
printf '$ openai'
printf ' %s' "${capture_flags[@]}"
printf '%s\n' ' \'
if [ "$DEMO_CAPTURE_KIND" = after-limits ]; then
  capture_flags+=(--debug-max-bytes 256 --debug-max-events 1)
  printf '%s\n' '    --debug-max-bytes 256 --debug-max-events 1 \'
fi
printf '%s\n' '    responses create --model fake-model \'
printf '%s\n' "    --input 'synthetic private prompt' --store=false \\"
printf '    --max-output-tokens 8 --stream=%s' "$capture_stream"
printf '%s\n' ' \'
if [ "$capture_stream" = true ]; then
  printf '%s\n' '    --format text --transform type --raw-output'
  openai "${capture_flags[@]}" responses create --model fake-model \
    --input 'synthetic private prompt' --store=false --max-output-tokens 8 --stream=true \
    --format text --transform type --raw-output
else
  printf '%s\n' '    --format json --transform usage'
  openai "${capture_flags[@]}" responses create --model fake-model \
    --input 'synthetic private prompt' --store=false --max-output-tokens 8 --stream=false \
    --format json --transform usage
fi
printf '\n$ '
sleep 2
