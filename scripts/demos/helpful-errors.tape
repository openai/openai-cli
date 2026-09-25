# Run through record.sh. The before binary is this PR's readable-output parent.
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
Type "# before: readable output | synthetic 401 error"
Enter
Type "openai models retrieve --model demo-auth"
Enter
Wait+Screen /invalid_api_key/
Sleep 500ms
Screenshot before.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$PATH"; clear`
Enter
Sleep 300ms
Show
Type "# after: helpful errors | same 401 error"
Enter
Type "openai models retrieve --model demo-auth"
Enter
Wait+Screen /help setup/
Sleep 500ms
Screenshot after.png
Sleep 4s
Hide
Type "clear"
Enter
Sleep 300ms
Show
Type "# full API error details are still available"
Enter
Type "openai --format-error json models retrieve --model demo-auth"
Enter
Wait+Screen /invalid_api_key/
Sleep 500ms
Screenshot explicit-json.png
Sleep 3s
