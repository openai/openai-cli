# Retained VHS recipe; record.sh uses asciinema + agg for the verified replay.
# Provide DEMO_BEFORE_DIR, DEMO_AFTER_DIR, and DEMO_API_URL (fixture origin).
# Start VHS with an isolated environment and OPENAI_API_KEY=synthetic-demo-key.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1498
Set Height 965
Set Padding 24
Set TypingSpeed 32ms
Set Theme "Dracula"
Env PS1 "$ "
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$PATH"; export OPENAI_BASE_URL="$DEMO_API_URL/normal/v1"; clear`
Enter
Sleep 300ms
Show
Type "# Before: main | synthetic metadata fixture"
Enter
Type "openai images models --all"
Enter
Sleep 1s
Screenshot before.png
Sleep 4s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# After: exact names, visibility, retirement and dated versions"
Enter
Type "openai images models --all"
Enter
Wait+Screen /generation permissions can differ/
Sleep 500ms
Screenshot after.png
Sleep 4s
