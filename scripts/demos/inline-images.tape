# Alternative VHS recipe. record-inline-images.sh uses asciinema on this machine.
# Put the tested binary on PATH as openai; use the synthetic loopback demo API.
Output inline-images.gif
Set Shell bash
Set FontSize 20
Set Width 1280
Set Height 820
Set TypingSpeed 35ms
Set Env TERM "xterm-256color"
Set Env TERM_PROGRAM ""
Type `openai images generate --prompt "A tiny orange robot"`
Enter
Sleep 3s
Type `openai images generate --prompt "A tiny orange robot" --inline off`
Enter
Sleep 3s
