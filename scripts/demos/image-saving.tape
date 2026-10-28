# Alternative VHS recipe. record-image-saving.sh uses asciinema + agg instead.
# Independently build before/after openai binaries and the synthetic main.go API.
# Supply DEMO_BEFORE_DIR and DEMO_AFTER_DIR with those binaries named openai,
# distinct empty DEMO_BEFORE_HOME, DEMO_AFTER_HOME and DEMO_JSON_HOME directories,
# OPENAI_BASE_URL for the loopback fixture, OPENAI_API_KEY=synthetic-demo-key,
# and DEMO_SYSTEM_PATH containing normal system tools. Run outside the checkout.
# Inspect every screenshot and GIF frame; this is not native-terminal validation.
Output comparison.gif
Output comparison.ascii
Set Shell "bash"
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1240
Set Height 720
Set Padding 24
Set TypingSpeed 32ms
Set Theme "Dracula"
Env PS1 "$ "
Env NO_COLOR "1"
Hide
Type `export PATH="$DEMO_BEFORE_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_BEFORE_HOME"; clear`
Enter
Sleep 300ms
Show
Type "# Before: pager-fix parent | synthetic image response"
Enter
Type `openai images generate --prompt "A tiny orange robot"`
Enter
Wait+Screen /base64 characters/
Sleep 500ms
Screenshot before.png
Sleep 2s
Hide
Type `export PATH="$DEMO_AFTER_DIR:$DEMO_SYSTEM_PATH" HOME="$DEMO_AFTER_HOME"; clear`
Enter
Sleep 300ms
Show
Type "# After: automatic saving | identical synthetic response"
Enter
Type `openai images generate --prompt "A tiny orange robot"`
Enter
Wait+Screen /tiny-orange-robot.png/
Sleep 500ms
Screenshot after.png
Sleep 2s
Hide
Type `export HOME="$DEMO_JSON_HOME"; clear`
Enter
Sleep 300ms
Show
Type "# Explicit JSON: API data, with no automatic saving"
Enter
Type `openai --format json images generate --prompt "A tiny orange robot"`
Enter
Wait+Screen /b64_json/
Sleep 500ms
Screenshot explicit-json.png
Sleep 2s
