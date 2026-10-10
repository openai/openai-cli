#!/bin/bash
set -euo pipefail
if [ -n "${DEMO_CAPTURE_REGISTRATION:-}" ]; then
  # The bounded supervisor owns this private directory and acknowledges this exact group.
  demo_group="$(/bin/ps -o pgid= -p "$$")"
  demo_group="${demo_group//[[:space:]]/}"
  [[ "$demo_group" =~ ^[1-9][0-9]*$ ]] || exit 95
  printf '%s %s\n' "$$" "$demo_group" > "$DEMO_CAPTURE_REGISTRATION/request.pending"
  mv "$DEMO_CAPTURE_REGISTRATION/request.pending" "$DEMO_CAPTURE_REGISTRATION/request"
  demo_registered=0
  for ((demo_attempt=0; demo_attempt<100; demo_attempt++)); do
    test ! -e "$DEMO_CAPTURE_REGISTRATION/../closed" || exit 95
    if [ -f "$DEMO_CAPTURE_REGISTRATION/ack" ]; then
      cmp -s "$DEMO_CAPTURE_REGISTRATION/request" "$DEMO_CAPTURE_REGISTRATION/ack" || exit 95
      demo_registered=1
      break
    fi
    sleep 0.05
  done
  test "$demo_registered" -eq 1 || exit 95
fi
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
