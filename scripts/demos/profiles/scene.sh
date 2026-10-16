#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK_ROOT"
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n' "Terminal replay | $DEMO_PLATFORM | offline synthetic profiles"

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
  demo_command list 3 profile list
else
  demo_command create 0 --format=json profile create work
  demo_command set 0 --format=json profile set work --project proj_demo
  demo_command activate 0 --format=json profile activate work
  demo_command switch 0 --format=json auth switch --to-project proj_other
  demo_command status 0 auth status
  demo_command inspect 0 profile get work
fi
printf '$ '
sleep 3
