#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
printf '%s\n' '$ openai files --help'
openai files --help
printf '\n$ '
sleep 4
