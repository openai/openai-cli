# Alternative VHS recipe. record.sh uses asciinema + agg instead.
# Provide DEMO_BEFORE_DIR, DEMO_AFTER_DIR, OPENAI_BASE_URL and the fake key.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 18
Set Width 1190
Set Height 910
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
Type "# Before: main | delayed synthetic stream"
Enter
Type "openai responses create --model demo-model --stream=true --input 'Say hello'"
Enter
Wait+Screen /Total tokens: 9/
Sleep 500ms
Screenshot before.png
Sleep 2s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Show
Type "# After: streaming text | identical delayed events"
Enter
Type "openai responses create --model demo-model --stream=true --input 'Say hello'"
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
Type "openai --format jsonl responses create --model demo-model --stream=true --input 'Say hello'"
Enter
Wait+Screen /total_tokens/
Sleep 500ms
Screenshot explicit-jsonl.png
Sleep 2s
