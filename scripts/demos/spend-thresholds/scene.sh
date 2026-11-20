#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL" 'Terminal replay. Synthetic data.'
printf '\n'
case "$DEMO_CASE" in
  limit|missing)
    printf '%s\n' '$ openai admin organization spend-limit retrieve'
    openai admin organization spend-limit retrieve
    ;;
  alerts)
    printf '%s\n' '$ openai admin organization projects spend-alerts list --project-id proj_demo --max-items 2'
    openai admin organization projects spend-alerts list --project-id proj_demo --max-items 2
    ;;
  json)
    printf '%s\n' '$ openai admin organization spend-limit retrieve --format json'
    openai admin organization spend-limit retrieve --format json
    ;;
  *) echo 'Unknown spend demo scene.' >&2; exit 2;;
esac
printf '\n$ '
sleep 2
