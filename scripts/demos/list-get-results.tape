# Retained VHS recipe; record.sh renders this scenario with asciinema + agg.
# Provide DEMO_BEFORE_DIR, DEMO_AFTER_DIR, and OPENAI_BASE_URL for demo-api.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1219
Set Height 660
Set Padding 24
Set TypingSpeed 32ms
Set Theme "Dracula"
Env PS1 "$ "
Env OPENAI_API_KEY "synthetic-demo-key"
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# Before: pager cleanup parent | two synthetic files"
Enter
Type "openai files list --max-items 2"
Enter
Wait+Screen /file_reference/
Sleep 500ms
Screenshot before.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# After: list/get summaries | same two files"
Enter
Type "openai files list --max-items 2"
Enter
Wait+Screen /file_reference/
Sleep 500ms
Screenshot after.png
Sleep 3s
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# Before: pager cleanup parent | retrieve the first file"
Enter
Type "openai files retrieve file_training"
Enter
Wait+Screen /file_training/
Sleep 500ms
Screenshot before-retrieve.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# After: list/get summaries | same file"
Enter
Type "openai files retrieve file_training"
Enter
Wait+Screen /Summary; use --format json for full data./
Sleep 500ms
Screenshot after-retrieve.png
Sleep 3s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "# Explicit JSON | full data for the same file"
Enter
Type "openai --format json files retrieve file_training"
Enter
Wait+Screen /created_at/
Sleep 500ms
Screenshot explicit-json.png
Sleep 3s
