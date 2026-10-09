#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H'
printf '%s\n' 'Terminal replay | synthetic data'
printf '%s\n\n' "$DEMO_SCENE_LABEL"
if [ "$DEMO_SCENE" = guided ]; then
  exec "$DEMO_PYTHON" -I -B "$DEMO_DRIVER"
elif [ "$DEMO_SCENE" = discovery ]; then
  printf '%s\n' '$ openai webhooks event-types list'
  demo_args=(webhooks event-types list)
else
  printf '%s\n' '$ openai webhooks test \' \
    '    --webhook-endpoint-id wh_demo \' \
    '    --event-type response.completed'
  demo_args=(webhooks test --webhook-endpoint-id wh_demo --event-type response.completed)
fi
if openai "${demo_args[@]}"; then demo_status=0; else demo_status=$?; fi
printf '%s\n' "$demo_status" > "$DEMO_STATUS_FILE"
printf '\n$ '
sleep 3
exit "$demo_status"
