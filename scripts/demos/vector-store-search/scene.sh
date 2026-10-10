#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic search response | indexing state unknown'
printf '%s\n' '$ openai vector-stores search \'
printf '%s\n' '    --vector-store-id vs_demo \'
printf '%s\n' '    --query hello'
openai vector-stores search --vector-store-id vs_demo --query hello
printf '\n$ '
sleep 2
