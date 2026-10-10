#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic local API | project rate limits'
printf '%s\n' '$ openai admin projects rate-limits list --project-id proj_empty'
if [ "$DEMO_SCENE" = before ]; then
  status=0
  openai admin projects rate-limits list --project-id proj_empty || status=$?
  test "$status" -eq 1
else
  openai admin projects rate-limits list --project-id proj_empty
fi
printf '\n%s\n' '$ openai admin projects rate-limits list-rate-limits --project-id proj_demo'
openai admin projects rate-limits list-rate-limits --project-id proj_demo
printf '\n%s\n' 'Existing command stays available. Explicit JSON preserves API field names.'
sleep 3
