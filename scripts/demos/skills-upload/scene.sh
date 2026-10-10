#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_INPUT_DIR"
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic Skills API | validates ZIP bytes, paths and executable mode'
for demo_input in demo-skill demo-skill.zip; do
  printf '$ openai skills create --files %s\n' "$demo_input"
  if openai skills create --files "$demo_input"; then demo_status=0; else demo_status=$?; fi
  test "$demo_status" -eq "$DEMO_EXPECTED_STATUS"
  printf '\n$ openai skills versions create --skill-id skill_demo --files %s\n' "$demo_input"
  if openai skills versions create --skill-id skill_demo --files "$demo_input"; then demo_status=0; else demo_status=$?; fi
  test "$demo_status" -eq "$DEMO_EXPECTED_STATUS"
  printf '\n'
done
if [ "$DEMO_EXPECTED_STATUS" -eq 0 ]; then
  printf '%s\n' 'Fixture verified: exact ZIP bytes; nested binary data; executable helper.'
fi
sleep 3
