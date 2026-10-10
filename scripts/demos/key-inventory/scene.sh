#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic inventory | no live keys or requests'
printf '%s\n' '$ openai admin admin-api-keys list'
openai admin admin-api-keys list
printf '\n%s\n' '$ openai admin projects api-keys list --project-id proj_demo'
openai admin projects api-keys list --project-id proj_demo
sleep 3
