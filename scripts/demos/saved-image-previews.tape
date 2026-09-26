# Alternative VHS recipe. The checked shell recorder uses the shared PTY lifecycle.
# Put the tested binary on PATH as openai, isolate HOME and provide robot.png.
# Use the fixed synthetic PNG retained by record-inline-images.sh.
Output saved-image-previews.gif
Set Shell bash
Set FontSize 20
Set Width 1280
Set Height 960
Set TypingSpeed 35ms
Set Env TERM "xterm-256color"
Set Env TERM_PROGRAM ""
Set Env OPENAI_API_KEY ""
Type `openai images inline off`
Enter
Sleep 2s
Type `openai images preview robot.png`
Enter
Sleep 3s
Type `openai images inline on`
Enter
Sleep 2s
