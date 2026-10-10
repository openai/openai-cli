#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2
printf '\033[2J\033[H'
printf '%s\n' 'Synthetic API | fine-tuning jobs'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
printf '%s\n' '$ openai fine-tuning jobs list'
openai fine-tuning jobs list
sleep 1
printf '\n%s\n' '$ openai fine-tuning jobs list --base-url "$OPENAI_BASE_URL/denied"'
if openai fine-tuning jobs list --base-url "$OPENAI_BASE_URL/denied"; then
  echo 'Expected the denied request to fail.' >&2
  exit 1
else
  test "$?" -eq 1
fi
sleep 2
