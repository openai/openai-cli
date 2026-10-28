# Retained VHS recipe; record.sh renders the same scenario with asciinema + agg.
# For VHS, provide DEMO_BEFORE_DIR, DEMO_AFTER_DIR, and the local fixture environment.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1120
Set Height 660
Set Padding 24
Set TypingSpeed 32ms
Set Theme "Catppuccin Mocha"
Env PS1 "$ "
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# before: main | synthetic local response"
Enter
Type "openai models retrieve --model demo-chat"
Enter
Wait+Screen /demo-project/
Sleep 500ms
Screenshot before.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# after: readable output | same response"
Enter
Type "openai models retrieve --model demo-chat"
Enter
Wait+Screen /demo-project/
Sleep 500ms
Screenshot after.png
Sleep 3s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "# full JSON is still available"
Enter
Type "openai --format json models retrieve --model demo-chat"
Enter
Wait+Screen /demo-project/
Sleep 500ms
Screenshot explicit-json.png
Sleep 3s
