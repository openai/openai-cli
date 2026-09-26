# Alternative VHS recipe. record-image-progress.sh uses asciinema on this machine.
# Put the tested binary on PATH as openai; use scripts/demos/main.go's loopback API.
Output image-progress.gif
Set Shell bash
Set FontSize 20
Set Width 1280
Set Height 1000
Set TypingSpeed 35ms
Set Env TERM "xterm-256color"
Set Env TERM_PROGRAM ""
Type `openai images generate --prompt "A tiny orange robot" --partial-images 2`
Enter
Sleep 5s
