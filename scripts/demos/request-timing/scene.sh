#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Terminal replay | synthetic loopback API | delayed headers and body'
sleep 0.4
printf '%s\n' '$ openai --debug models retrieve model_synthetic'
openai --debug models retrieve model_synthetic
printf '\n$ '
sleep 3
