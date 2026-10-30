# Speech controls and retained audio

Run this command with terminal input and output:

```sh
openai audio speak
```

Type in the editor below **Text to speech**. The caret marks the insertion point as text wraps.
Instructions and Voice stay on the form. Settings summarizes the model, format, and speed.
Select Settings and press Enter to edit those three fields inline. Escape collapses Settings and keeps applied values.
Model and format pickers show the applied value under Current. Arrow keys browse choices. Enter applies the highlighted choice.
Wide terminals show descriptions beside each choice. Smaller terminals show the highlighted choice's description below the list.
Each new form fills Instructions with the editable example: “Speak warmly and clearly.”
The example is real text. The form sends it when the selected model supports Instructions.
Clear form empties both editors. It does not restore the example.

Tab moves through Text, Instructions, Settings, then Audio. Wide terminals include Actions between Settings and Audio.
Shift+Tab reverses that order.
Up and Down move between sections at their boundaries.
Inside an editor, arrows move through multiline and wrapped text before leaving the first or last visual row.
Modified arrows and active selections remain inside the editor.
Up from Voice returns to Instructions when the model supports them.
Down from the bottom row of the form moves to Audio. It focuses the waveform when the recording has one.
When Generate speech, History, and More actions share a row, Left and Right move between them.
Home and End select the first and last settings.
Enter opens a choice or starts generation from an editor.
Enter on Speed accepts an exact number from 0.25 to 4. Left and Right adjust speed by 0.25.
Alt+Enter adds a newline. Ctrl+G generates from any ordinary form section.
Ctrl+S opens Save for the current recording. Before generation, it shows “Generate speech first.”
Ctrl+O focuses Audio without starting playback.
Navigation preserves draft text and playback. Navigation never generates speech.

The form disables Instructions for `tts-1` models and keeps the draft while you change models.
Navigation skips that disabled editor.
These models omit Instructions from the request, command text, and retained History entry.
Returning to a compatible model restores access to the instruction draft.
Voice choices follow the form's existing model validator.
Changing models preserves the selected voice. An unavailable voice needs another explicit choice before generation.

Generate speech is the primary action. At 110 columns and 24 rows, Actions shares its row.
Press Right from Generate speech or the Settings heading to enter Actions. Tab from Settings also enters Actions.
Left and Right move between actions. Enter opens the selected action. Home selects Generate. End selects the last action.
Up returns to Settings. Down moves to Audio. Up from the waveform returns to the action you left.
Actions contains History, Preview voice, Clear form, View command, and Help.
On smaller terminals, select More actions and press Enter.
View command opens a vertically scrollable Bash/zsh command.
Up/Down and PageUp/PageDown scroll it. Home/End select its start or end.
Escape returns to More actions with View command selected.
Tab focuses Audio. Shift+Tab returns to the More actions control.
Press `c` in View command to copy the complete Bash/zsh command and stay in the form.
Copy uses the shell text, including line continuations. It excludes the title, colors, and display wrapping.
Local macOS sessions use the system pasteboard. Other sessions request clipboard access from the terminal.
Some terminals decline that request. Use Ctrl+P to print the command in those sessions.
Ctrl+P prints the same command to the terminal and exits. Neither action generates speech.
A prior failed action keeps its error and unsuccessful exit status, even when printing succeeds.
Cancellation or cleanup failures detected before printing prevent output. Later output or closure failures still exit unsuccessfully.
The command preserves supported project, organization, and endpoint overrides.
Private credential overrides suppress the command instead of exposing their values.
The editors normalize line endings, expand tabs, and remove unsupported control and replacement characters.
Use direct flags or file inputs to preserve text that the editors normalize.
Existing `@file` expansion and escaped `\@` literal input rules still apply to direct commands.

Choosing a model or voice does not generate speech.
Preview voice explicitly generates and plays a short sample using the selected settings.
An identical retained sample can play again without another API request.
With the audition result focused, Esc restores a previous result if one exists, then returns to Text.
Normal generation also requires an explicit action.

The form retains completed audio, request inputs, and settings in private local History.
It stores History under the operating system's user cache directory, within `openai/audio`.
History can contain sensitive text.
Clear form resets the draft and current result. It preserves History and exported files.

The form shows local validation beside the field that needs attention.
If generation fails with a previous recording available, the player keeps that recording and shows the cause.
Press F1 for recovery details. Press Ctrl+G to retry when ready.
A generation failure without a previous recording opens Error details directly.
Multiple instructions appear in order. A missing API key shows one setup command using the same CLI invocation.
For an installed CLI, the command is `openai help setup`.
When you launch with `go run ./cmd/openai`, the command is `go run ./cmd/openai help setup`.
Up and Down scroll long messages. Escape restores the interrupted form or dialog.
More actions includes Last error while that failure remains current. F1 also reopens Error details.
Viewing or dismissing an error never retries the request. The command retains its original exit status.

Completed results show their format and byte count.
When analysis succeeds, they also show a waveform derived from decoded samples and duration.
Result labels retain the original generation settings when you change the form.
Left and Right select Play audio or Save audio. Enter performs the selected action.
Up focuses the waveform. Down returns to the selected action.
On the waveform, Left and Right move the playback position. Shift moves five waveform bars.
With a measured recording focused, press G to enter a time. Typing a digit also starts time entry.
Enter seconds such as `2.5` or a timestamp such as `1:02.500`. Enter applies the time. Esc cancels.
Invalid input restores the current position in the field. You can type a replacement immediately.
Playback continues while you edit. Selecting a different position stops playback.
Space plays from the selected position. Once the player reports progress in a measured recording, Space pauses.
Resume starts playback from the last reported position. Otherwise the control shows Stop.
The marker follows player-reported progress. Players without progress reports keep a stationary marker.
With the result focused, S opens Save and H opens History.
Save exports the complete original audio, including when playback starts from a selected position.
The default destination is `~/Downloads/gpt-audio`, with a filename derived from the retained prompt.
Save opens with the Save to field focused. Edit the folder or keep the suggested destination.
Press Enter to focus Save audio. Press Enter again to write the file.
Tab moves between Filename, Save to, and Save audio. Default-name collisions receive numbered filenames.
History is available directly in the form and through More actions.
History shows prompt previews and generation metadata. Opening an entry restores its settings without generation.

Native WAV analysis supports PCM at 8, 16, 24, or 32 bits and floating-point samples at 32 or 64 bits.
API PCM also supports waveform analysis without external tools.
MP3, Opus, AAC, FLAC, and other WAV variants require FFmpeg on `PATH`.
Those WAV variants include compressed encodings, RF64, and streaming headers with unknown lengths.
Playback requires FFplay. Audio requiring external decoding also needs FFmpeg.
The CLI does not install either tool.
Missing tools do not delete retained audio or prevent exporting its original bytes.
Replay starts a completed recording from the beginning.

The model picker distinguishes five documented Speech model IDs from two legacy Playground IDs.
The Speech API names `gpt-4o-mini-tts`, its March and December 2025 snapshots, `tts-1`, and `tts-1-hd`.
OpenAI lists January 6, 2027 as their shutdown date. [Speech API reference](https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create)
The CLI also accepts `tts-1-1106` and `tts-1-hd-1106` for compatibility. The current model catalog does not list those IDs.
Project access can vary. [TTS-1 catalog](https://developers.openai.com/api/docs/models/tts-1) · [TTS-1 HD catalog](https://developers.openai.com/api/docs/models/tts-1-hd)
The form rejects unsupported voices for the `tts-1` family before generation.
It keeps unsupported Instructions in the draft and omits them from requests.
Direct flags retain their existing API behavior, including custom voices and future model IDs.

OpenAI recommends `gpt-realtime-2.1-mini` through the Realtime API for new integrations.
This form uses the Speech endpoint. [Deprecation schedule](https://developers.openai.com/api/docs/deprecations#2026-10-01-text-to-speech-models)

Explicit flags, piped input, and explicit output modes retain the existing direct command path:

```sh
openai audio speak --model gpt-4o-mini-tts --voice coral --input 'Hello.' --instructions 'Speak warmly.' --response-format wav --speed 1 --output hello.wav

# Preserve exact binary stdout for a shell pipeline.
openai audio speak --model gpt-4o-mini-tts --voice coral --input 'Hello.' --response-format mp3 --output - > hello.mp3
```

Direct commands keep their existing save behavior. They do not enter the form or add private history records.
Speech SSE, explicit machine formats, and existing transcription and translation flags remain available.

Local result commands use IDs from history:

```sh
openai audio history
openai --format json audio history
openai audio inspect AUDIO_ID
openai audio play AUDIO_ID
openai audio export AUDIO_ID --output saved.wav
openai audio export AUDIO_ID --output - > saved.wav

# Delete owned local history, including retained auditions.
# Previously exported files remain unchanged.
openai audio history --clear

# Print the embedded audio editor dependency notice.
openai audio licenses
```

These local commands need no API key and send no API request.
The export command preserves the original response bytes, regardless of the destination extension.
Inspect supports existing machine formats and extraction; JSON includes measured peaks and duration.
History lists ordinary results and hides cached auditions.
Damaged history records produce an error while valid records remain available.

Ctrl+C cancels pending work. The CLI closes owned media helpers before exiting.
If terminal output remains blocked, cancellation still restores native terminal modes without waiting indefinitely.
The blocked output can prevent cursor, bracketed-paste, and screen cleanup sequences from reaching the terminal.
Failed analysis or playback never triggers another generation request.
The CLI retains completed audio when optional analysis fails.

The [speech API reference](https://developers.openai.com/api/reference/resources/audio/subresources/speech/methods/create) defines request controls.
The [text-to-speech guide](https://developers.openai.com/api/docs/guides/text-to-speech) documents formats and model-specific voices.
See [transcript output](transcript-output.md) for finite transcription and translation presentation.

## Live microphone and browser-tab translation

```sh
openai audio translate --live
```

This explicit mode opens a private local browser page. It keeps your API key in the CLI.
Choose Microphone or Browser tab, select a target language, then start the session.
For tab capture, choose a browser tab and enable Share tab audio in the browser dialog.
The CLI sends audio only. It does not send the shared video track.
Tab-audio support depends on the browser and operating system.

The page exposes the target language, microphone, output device, playback, and noise reduction controls.
It uses `gpt-realtime-translate` for translation and `gpt-realtime-whisper` for input transcription.
Device selection depends on browser support. Unsupported output selection stays disabled.
Stop session flushes captured audio and waits for final translation updates.
Stop now interrupts that wait and retains received output.
The page reports interrupted capture instead of claiming successful completion.

Original and translated captions appear beside the measured waveform.
Play, Pause, Replay, waveform clicks, and arrow keys use the recording's actual position.
Save audio exports the complete translated WAV. Save transcript exports both caption tracks.
The browser controls the download destination. Filenames derive from translated text.
A failed replacement attempt preserves the previous recording until new translated output arrives.
Clear removes the current browser recording. Browser recordings do not enter speech History.
Ctrl+C closes the local page's connection and removes its private launch file.

The live page currently supports microphone and browser-tab audio.
Typed text and file upload remain separate workflows.
Existing file translation keeps its input, output, and English translation behavior:

```sh
openai audio translate --file recording.wav --model whisper-1
```

Speech History groups recordings by local date and shows one prompt row per recording.
Type in History to filter by clip text, voice, or format. Search ignores letter case.
Up from the first recording focuses Search. Down selects a matching recording. Enter opens it.
Backspace removes one character. Ctrl+U clears Search.
Roomy terminals show timestamps and selected-recording metadata.
Compact terminals preserve prompt space and keep the selected recording visible.
More actions lists secondary commands and brief descriptions when space permits.
Escape returns to the form.

## Layout

At 60 columns and 24 rows, the draft, settings, and audio share one frame.
A vertical line marks the script editor. It turns blue while you type.
At 100 columns, Delivery sits beside the script and groups voice, model, format, and speed.
Narrower frames place those controls below Instructions.
Below 110 columns, Generate speech, History, and More actions share a row when space permits.
Wider frames show Generate speech, recording actions, View command, and Help in one row.
The selection marker moves across this row without shifting neighboring actions.
Help groups shortcuts by Compose, Audio, and View command. Small terminals show scrolling controls when the list exceeds the available height.
Clear form uses amber because it resets the draft. Last error uses red when recovery details are available.
The focused action has a selection marker. In color terminals, it also has a selected background.

The Batch browser’s thin divider separates the form from the player. Its brightest cell sits at the center.
Before generation, the framed player shows “No recording yet,” Play, Save, and a zero timer.
Playback and Save shortcuts explain that speech must be generated first.
The recording adds a measured waveform and seek rail in that location.
When space permits, a time ruler labels the start, midpoint, and end in minutes and seconds.
Recordings without a measured duration show an unknown timer and retain their Save action.
One blank row separates the recording details from the waveform when space permits.
Measured audio uses a continuous centerline and a seek rail with exact endpoints.
The playhead follows playback through silence. Elapsed bars and rail use green. Upcoming bars use the secondary text color.
Long bars fade toward both tips. Their shades reflect peak heights between terminal half-cell steps.
Short bars keep their contrast. Elapsed time uses blue.
Amber marks a paused or manually positioned playhead. The status describes its state.
The heavier rail shows the played portion in monochrome. The final marker turns green when playback completes.
Play starts under the first timeline marker. Save and the timer follow in the same row.
When space permits, one blank row separates the waveform from the seek rail.
A second spare row separates the rail from the controls.
When the timer needs more space, it moves above the actions and aligns with the final marker.
Save opens the destination picker. A successful export adds its path below the actions.

Smaller terminals use stacked controls. The minimum terminal size is 40 columns and 12 rows.
Shortcut keys use the accent color. Their descriptions use the secondary text color.
