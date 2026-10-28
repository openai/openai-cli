#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_WORK"
printf '\033[2J\033[H%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic loopback API | three fake requests | no charges'
case "$DEMO_SCENE" in
  before)
    printf '%s\n' '$ openai files upload --file requests.jsonl --purpose batch'
    openai files upload --file requests.jsonl --purpose batch
    printf '\n%s\n' '$ openai batches create --input-file-id file_input_demo --endpoint /v1/responses --completion-window 24h'
    openai batches create --input-file-id file_input_demo --endpoint /v1/responses --completion-window 24h
    ;;
  after-submit)
    printf '%s\n' '$ openai batches submit --file requests.jsonl --endpoint /v1/responses --completion-window 24h'
    openai batches submit --file requests.jsonl --endpoint /v1/responses --completion-window 24h
    printf '\n%s\n' '$ openai batches retrieve batch_demo'
    openai batches retrieve batch_demo
    ;;
  after-results)
    printf '%s\n' '$ openai batches inputs --search requests.jsonl'
    openai batches inputs --search requests.jsonl
    printf '\n%s\n' '$ openai batches results batch_demo --custom-id sentiment-positive --match-input'
    openai batches results batch_demo --custom-id sentiment-positive --match-input
    ;;
  *) exit 2 ;;
esac
printf '\n$ '
sleep 3
