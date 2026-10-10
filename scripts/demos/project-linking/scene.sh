#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK_ROOT"
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n' "Terminal replay | $DEMO_PLATFORM | synthetic remote Files API"
printf '%s\n' '$ cd work-chatbot'
cd work-chatbot

demo_command() {
  local demo_name="$1" demo_expected="$2" demo_status
  shift 2
  printf '$ openai'
  printf ' %s' "$@"
  printf '\n'
  sleep 0.4
  if openai "$@"; then demo_status=0; else demo_status=$?; fi
  printf '%s\t%s\t%s\n' "$DEMO_SCENE" "$demo_name" "$demo_status" >> "$DEMO_STATUS_LOG"
  if [ "$demo_status" -ne "$demo_expected" ]; then
    return 97
  fi
  sleep 0.8
}

if [ "$DEMO_SCENE" = before ]; then
  demo_command link 3 link --project proj_work
else
  demo_command link 0 link --project proj_work
  demo_command inspect 0 link
  demo_command files 0 files list
  demo_command unlink 0 unlink
fi
printf '$ '
sleep 3
