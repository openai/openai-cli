# Alternative VHS recipe. record.sh uses asciinema + agg instead.
# Provide DEMO_BEFORE_DIR, DEMO_AFTER_DIR, OPENAI_BASE_URL and the fake key.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1220
Set Height 860
Set Padding 20
Set TypingSpeed 25ms
Set Theme "Dracula"
Env PS1 "$ "
Env FORCE_COLOR "0"
Env NO_COLOR "1"
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$PATH"; clear`
Enter
Show
Type "# Before: streaming parent #279 | delayed synthetic stream"
Enter
Type "openai audio:transcriptions create --file sample.wav --model demo-model --stream=true"
Enter
Wait+Screen /Total tokens: 9/
Sleep 500ms
Screenshot before.png
Sleep 2s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Show
Type "# After: readable audio | identical delayed events"
Enter
Type "openai audio:transcriptions create --file sample.wav --model demo-model --stream=true"
Enter
Wait+Screen /Total tokens: 9/
Sleep 500ms
Screenshot after.png
Sleep 2s
Hide
Type "clear"
Enter
Show
Type "# Explicit JSONL: every original event remains available"
Enter
Type "openai --format jsonl audio:transcriptions create --file sample.wav --model demo-model --stream=true"
Enter
Wait+Screen /total_tokens/
Sleep 500ms
Screenshot explicit-jsonl.png
Sleep 2s
