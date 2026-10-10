#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic Costs API | two pages | exact project totals'
sleep 0.4
printf '%s\n' '$ openai costs report --from 2026-10-01 --to 2026-10-08 \'
printf '%s\n' '    --timezone UTC --group-by project'
if openai costs report --from 2026-10-01 --to 2026-10-08 --timezone UTC --group-by project; then
  demo_status=0
else
  demo_status=$?
fi
printf '%s\t%s\n' "$DEMO_SCENE" "$demo_status" >> "$DEMO_STATUS_LOG"
printf '\n[exit status: %s]\n' "$demo_status"
test "$demo_status" -eq "$DEMO_EXPECTED_STATUS"
printf '\n$ '
sleep 2
