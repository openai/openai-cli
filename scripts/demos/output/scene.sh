#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_DATA_DIR" || exit 98

say() {
  printf "$@" || exit 98
}

record_status() {
  printf '%s\t%s\t%s\n' "$DEMO_SCENE" "$1" "$2" >> "$DEMO_STATUS_LOG" || exit 98
  test "$2" -eq "$3" || exit 97
}

say '\033[2J\033[H\033[?25l'
say '%s\n' "$DEMO_SCENE_LABEL"
say '%s\n\n' 'Synthetic local API; real CLI pipes and files.'
sleep 0.5 || exit 98

if [ "$DEMO_SCENE" = controls ]; then
  say '%s\n' 'Quiet keeps the selected data.'
  say '%s\n' '$ openai --quiet models retrieve demo-model > quiet.txt 2> quiet.stderr'
  if openai --quiet models retrieve demo-model > quiet.txt 2> quiet.stderr; then demo_status=0; else demo_status=$?; fi
  record_status quiet "$demo_status" 0
  say '%s\n' '$ cat quiet.txt'
  cat quiet.txt || exit 98
  say '\n'
  sleep 1 || exit 98

  say '%s\n' 'Verbose details stay on stderr.'
  say '%s\n' '$ openai --verbose models retrieve demo-model > model.txt 2> details.txt'
  if openai --verbose models retrieve demo-model > model.txt 2> details.txt; then demo_status=0; else demo_status=$?; fi
  record_status verbose "$demo_status" 0
  say '%s\n' '$ cat model.txt'
  cat model.txt || exit 98
  say '%s\n' '$ cat details.txt'
  cat details.txt || exit 98
else
  say '%s\n' 'Piped JSON reaches a standard parser.'
  say '%s\n' '$ FORCE_COLOR=1 openai --format json models retrieve demo-model \'
  say '%s\n' '    | tee model.json | python3 -m json.tool'
  FORCE_COLOR=1 openai --format json models retrieve demo-model | tee model.json | python3 -m json.tool
  demo_pipeline=("${PIPESTATUS[@]}")
  record_status json-cli "${demo_pipeline[0]}" 0
  record_status json-tee "${demo_pipeline[1]}" 0
  demo_parser_status=0
  if [ "$DEMO_SCENE" = before ]; then demo_parser_status=1; fi
  record_status json-parser "${demo_pipeline[2]}" "$demo_parser_status"
  say '\n'
  sleep 1 || exit 98

  say '%s\n' 'The API sends its second event two seconds later.'
  say '%s\n' '$ openai --format jsonl responses create --model demo-model \'
  say '%s\n' "    --input 'Say hi' --stream=true | python3 arrival.py"
  openai --format jsonl responses create --model demo-model --input 'Say hi' --stream=true | python3 arrival.py
  demo_pipeline=("${PIPESTATUS[@]}")
  record_status stream-cli "${demo_pipeline[0]}" 0
  record_status stream-consumer "${demo_pipeline[1]}" 0
  say '%s\n' 'Times above come from the Python consumer.'
fi
say '\n$ '
sleep 3 || exit 98
