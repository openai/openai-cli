# Alternative VHS recipe; actual macOS replay uses record-image-saving-recovery.sh.
# Supply separately built before/after binaries named openai in DEMO_BEFORE_DIR /
# DEMO_AFTER_DIR, DEMO_SYSTEM_PATH, four fresh HOME dirs, and the synthetic main.go
# fixture's OPENAI_BASE_URL with OPENAI_API_KEY=synthetic-demo-key.
Output comparison.gif
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1450
Set Height 580
Set Padding 24
Set Theme "Dracula"
Env PS1 "$ "
Env NO_COLOR "1"
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_BEFORE_HOME"; clear`
Enter
Show
Type `openai images generate --prompt "A tiny orange robot" --stream=true --max-items -1`
Enter
Wait+Screen /--max-items could stop before/
Screenshot before.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_AFTER_HOME"; clear`
Enter
Show
Type `openai images generate --prompt "A tiny orange robot" --stream=true --max-items -1`
Enter
Wait+Screen /Saved image:/
Screenshot after.png
Sleep 3s
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_BEFORE_RECOVERY_HOME"; clear`
Enter
Show
Type `openai images generate --prompt "A tiny orange robot" --count 2 | true`
Enter
Wait+Screen /The listed files are kept/
Screenshot before-recovery.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_AFTER_RECOVERY_HOME"; clear`
Enter
Show
Type `openai images generate --prompt "A tiny orange robot" --count 2 | true`
Enter
Wait+Screen /Check the output folder/
Screenshot after-recovery.png
Sleep 3s
