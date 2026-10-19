#!/bin/bash
set -euo pipefail
test -t 0 && test -t 1 && test -t 2 || exit 99
cd "$DEMO_INPUT_DIR"
printf '\033[2J\033[H'
printf '%s\n' "$DEMO_SCENE_LABEL"
printf '%s\n\n' 'Synthetic Files API | exact local filename and bytes'
sleep 0.4
if [ "$DEMO_SCENE" = before ]; then
  printf '%s\n' '$ openai files create --file "upload space.txt" --purpose user_data'
  openai files create --file "upload space.txt" --purpose user_data
  demo_get=retrieve
  demo_download=content
else
  printf '%s\n' '$ openai files upload "upload space.txt" --purpose user_data'
  openai files upload "upload space.txt" --purpose user_data
  demo_get=get
  demo_download=download
fi
printf '\n$ openai files %s file-example\n' "$demo_get"
sleep 0.4
openai files "$demo_get" file-example
printf '\n$ openai files %s file-example --output "downloaded copy.txt"\n' "$demo_download"
openai files "$demo_download" file-example --output "downloaded copy.txt"
printf '$ openai files %s file-example > copy.txt\n' "$demo_download"
openai files "$demo_download" file-example > copy.txt
printf '%s\n' '$ cmp "upload space.txt" "downloaded copy.txt" && cmp "upload space.txt" copy.txt'
cmp "upload space.txt" "downloaded copy.txt"
cmp "upload space.txt" copy.txt
printf '%s\n' 'Text: both downloads match all 13 bytes.'
printf '\n$ openai files %s file-binary --output copy.bin\n' "$demo_download"
openai files "$demo_download" file-binary --output copy.bin
printf '$ openai files %s file-binary > redirected.bin\n' "$demo_download"
openai files "$demo_download" file-binary > redirected.bin
printf '%s\n' '$ cmp source.bin copy.bin && cmp source.bin redirected.bin'
cmp source.bin copy.bin
cmp source.bin redirected.bin
printf '%s\n' 'Binary: both downloads match all 12 bytes.'
printf '\n$ '
sleep 4
