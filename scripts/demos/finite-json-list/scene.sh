#!/bin/bash
set -uo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_DATA_DIR" || exit 98

say() { printf "$@" || exit 98; }
record_status() {
  printf '%s\t%s\t%s\n' "$DEMO_SCENE" "$1" "$2" >> "$DEMO_STATUS_LOG" || exit 98
  test "$2" -eq "$3" || exit 97
}

say '\033[2J\033[H'
say '%s\n' "$DEMO_SCENE_LABEL"
say '%s\n\n' 'Synthetic local API | two files across two pages'
demo_expected=0
if [ "$DEMO_SCENE" = before ]; then demo_expected=1; fi

say '%s\n' '$ openai files list --format json > files.json'
if openai files list --format json > files.json 2> files.stderr; then demo_status=0; else demo_status=$?; fi
record_status files-cli "$demo_status" 0
say '%s\n' '$ cat files.json'
cat files.json || exit 98
say '%s\n' '$ python3 check.py array files.json'
python3 -I -B check.py array files.json
record_status files-parser "$?" "$demo_expected"
sleep 1 || exit 98

say '\n%s\n' '$ openai files list --purpose batch --format json > empty.json'
if openai files list --purpose batch --format json > empty.json 2> empty.stderr; then demo_status=0; else demo_status=$?; fi
record_status empty-cli "$demo_status" 0
say '%s\n' '$ cat empty.json'
cat empty.json || exit 98
say '%s\n' '$ python3 check.py array empty.json'
python3 -I -B check.py array empty.json
record_status empty-parser "$?" "$demo_expected"
sleep 1 || exit 98

say '\n%s\n' 'Use --format jsonl for one JSON value per line.'
say '%s\n' '$ openai files list --format jsonl > files.jsonl'
if openai files list --format jsonl > files.jsonl 2> jsonl.stderr; then demo_status=0; else demo_status=$?; fi
record_status jsonl-cli "$demo_status" 0
say '%s\n' '$ cat files.jsonl'
cat files.jsonl || exit 98
say '%s\n' '$ python3 check.py jsonl files.jsonl'
python3 -I -B check.py jsonl files.jsonl
record_status jsonl-parser "$?" 0
say '\n$ '
sleep 3 || exit 98
