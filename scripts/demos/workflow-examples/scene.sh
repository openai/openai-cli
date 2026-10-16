#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
case "$DEMO_TOPIC" in files|audio|models) ;; *) exit 98;; esac
unset OPENAI_API_KEY
cd "$HOME"
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Terminal replay | offline | no API calls'
printf '$ openai examples %s\n' "$DEMO_TOPIC"
if openai examples "$DEMO_TOPIC"; then demo_status=0; else demo_status=$?; fi
printf '\n$ '
sleep 3
exit "$demo_status"
