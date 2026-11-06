#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic project API | no account changes'
printf '%s\n' '$ openai admin projects create --name Demo --residency GLOBAL'
openai admin projects create --name Demo --residency GLOBAL
printf '\n%s\n' '$ openai admin projects update --project-id proj_demo --name Renamed'
openai admin projects update --project-id proj_demo --name Renamed
printf '\n%s\n' '$ openai admin projects archive --project-id proj_demo'
openai admin projects archive --project-id proj_demo
printf '\n$ '
sleep 3
