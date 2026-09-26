# Alternative VHS recipe; actual macOS replay uses record-image-edit-saving.sh.
# Build real before/after binaries named openai in DEMO_BEFORE_DIR/DEMO_AFTER_DIR.
# Supply DEMO_SYSTEM_PATH, DEMO_INPUT_DIR containing the synthetic photo.png and
# mask.png from record-image-edit-saving.sh, five fresh HOME directories,
# and the main.go fixture's OPENAI_BASE_URL with OPENAI_API_KEY=synthetic-demo-key.
Output comparison.gif
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1450
Set Height 640
Set Padding 24
Set Theme "Dracula"
Env PS1 "$ "
Env NO_COLOR "1"
Hide
Type `cd "$DEMO_INPUT_DIR"; export PATH="$DEMO_BEFORE_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_BEFORE_HOME"; clear`
Enter
Show
Type `openai images edit --image photo.png --mask mask.png --prompt "Make the sky purple"`
Enter
Wait+Screen /base64 characters/
Screenshot before.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_AFTER_HOME"; clear`
Enter
Show
Type `openai images edit --image photo.png --mask mask.png --prompt "Make the sky purple"`
Enter
Wait+Screen /make-the-sky-purple.png/
Screenshot after.png
Sleep 3s
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_BEFORE_VARIATION_HOME"; clear`
Enter
Show
Type `openai images create-variation --image photo.png`
Enter
Wait+Screen /synthetic-variation.png/
Screenshot before-variation.png
Sleep 3s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_AFTER_VARIATION_HOME"; clear`
Enter
Show
Type `openai images create-variation --image photo.png`
Enter
Wait+Screen /image-variation.png/
Screenshot after-variation.png
Sleep 3s
Hide
Type `export HOME="$DEMO_JSON_HOME"; clear`
Enter
Show
Type `openai --format json images edit --image photo.png --mask mask.png --prompt "Make the sky purple"`
Enter
Wait+Screen /b64_json/
Screenshot explicit-json.png
Sleep 3s
