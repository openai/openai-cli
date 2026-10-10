#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
unset OPENAI_API_KEY OPENAI_ADMIN_KEY OPENAI_WEBHOOK_SECRET
cd "$HOME"
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Terminal replay | offline'
case "$DEMO_DISCOVERY_MODE" in
  index)
    printf '%s\n' '$ openai examples'
    if openai examples; then demo_status=0; else demo_status=$?; fi
    printf 'index: %s\n' "$demo_status" > "$DEMO_STATUS_FILE"
    ;;
  recovery)
    printf '%s\n' '$ openai examples files --format yaml --format-error text'
    if openai examples files --format yaml --format-error text; then demo_status=0; else demo_status=$?; fi
    printf 'format failure: %s\n' "$demo_status" > "$DEMO_STATUS_FILE"
    test "$demo_status" -eq 1 || exit 98
    if [ "$DEMO_ROLE" = after ]; then
      # Recording pacing keeps guidance visible before narrow-view scrolling.
      sleep 1
      # Copy the independently captured diagnostic. Never evaluate recipe output.
      demo_recovery="$(cat "$DEMO_RECOVERY_FILE")"
      test "$demo_recovery" = 'openai examples files --format text' || exit 97
      printf '\n$ %s\n' "$demo_recovery"
      if /bin/bash --noprofile --norc -c "$demo_recovery"; then demo_status=0; else demo_status=$?; fi
      printf 'copied recovery: %s\n' "$demo_status" >> "$DEMO_STATUS_FILE"
    else
      printf '\n%s\n' '# No Try command was printed.'
    fi
    ;;
  *) exit 96;;
esac
printf '\n# %s: command finished\n$ ' "$DEMO_ROLE_LABEL"
sleep 3
exit "$demo_status"
