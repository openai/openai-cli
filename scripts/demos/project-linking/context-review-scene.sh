#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK_ROOT"
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf 'Terminal replay | synthetic settings\n'

demo_run() {
  local demo_name="$1" demo_expected="$2" demo_display="$3" demo_status
  shift 3
  printf '$ %s\n' "$demo_display"
  if "$@"; then demo_status=0; else demo_status=$?; fi
  printf '%s\t%s\t%s\n' "$DEMO_SCENE" "$demo_name" "$demo_status" >> "$DEMO_STATUS_LOG"
  test "$demo_status" -eq "$demo_expected"
  sleep 0.6
}

if [ "$DEMO_PROFILE" = normal ]; then
  demo_run environment 0 "OPENAI_CUSTOM_HEADERS='OpenAI-Project: proj_work' openai link" \
    env 'OPENAI_CUSTOM_HEADERS=OpenAI-Project: proj_work' openai link
fi
demo_run save 0 'openai link --project proj_folder --quiet' openai link --project proj_folder --quiet
if [ "$DEMO_PROFILE" = normal ]; then
  demo_run header 0 "openai link --header 'OpenAI-Project: proj_once'" \
    openai link --header 'OpenAI-Project: proj_once'
fi
printf '# Synthetic malformed registry\n'
printf '{' > "$DEMO_REGISTRY"
demo_run failure 1 'openai files list' openai files list
printf '$ '
sleep 2
