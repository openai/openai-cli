# Speech controls and retained audio

Run this command with terminal input and output:

```sh
openai audio speak
```

The form exposes Text, Instructions, Model, Voice, Format, and Speed.
Each new form fills Instructions with the editable example: “Speak warmly and clearly.”
The example is real text. The form sends it when the selected model supports Instructions.
Clear form empties both editors. It does not restore the example.

Tab follows the visible order: Text, Instructions, Settings, then Result when present.
Shift+Tab reverses that order.
Up and Down move between sections at their boundaries.
Inside an editor, arrows move through multiline and wrapped text before leaving the first or last visual row.
Modified arrows and active selections remain inside the editor.
Up from Model returns to Instructions. Down from More actions moves to Result when present.
Home and End select the first and last settings.
Enter opens a choice or starts generation from an editor.
Enter on Speed accepts an exact number from 0.25 to 4. Left and Right adjust speed by 0.25.
Alt+Enter adds a newline. Ctrl+G generates from any ordinary form section.
Navigation preserves draft text and playback. Navigation never generates speech.

The form disables Instructions for `tts-1` models and keeps the draft while you change models.
Navigation skips that disabled editor.
These models omit Instructions from the request, command text, and retained History entry.
Returning to a compatible model restores access to the instruction draft.
Voice choices follow the form's existing model validator.
Changing models preserves the selected voice. An unavailable voice needs another explicit choice before generation.

Generate speech is the primary action.
More actions contains History, Preview voice, Clear form, Show command, and Help.
The form keeps command text and help inside More actions.
Show command opens a vertically scrollable Bash/zsh command.
Up/Down and PageUp/PageDown scroll it. Home/End select its start or end.
Enter inside this view never generates speech.
Ctrl+P prints the complete command after frame cleanup and mode restoration, then exits without generating speech.
Copy the printed command from the terminal transcript for Bash or zsh.
The form's wrapped preview remains available for inspection.
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

Failed actions open recovery choices with a plain-language cause and a safe next action.
A confirmed key rejection says: “The API did not accept this key.”
Other authentication failures say: “The API could not verify your access.”
Authentication and permission failures offer API setup, which opens local setup help without checking credentials or sending requests.
Error details shows the complete guidance, numeric HTTP status, and validated request ID when available.
Up/Down scroll the details. Details opened from recovery return there with Escape.
Back to form returns to the interrupted dialog or the form's More actions setting.
More actions includes Last error while that failure remains current.
F1 remains a shortcut to Error details.
Opening setup help or details never retries the request.
The form keeps the original error for its existing exit diagnostics and status.

Completed results show their format and byte count.
When analysis succeeds, they also show a waveform derived from decoded samples and duration.
Result labels retain the original generation settings when you change the form.
Left and Right select Play audio or Save audio. Enter performs the selected action.
Up focuses the waveform. Down returns to the selected action.
On the waveform, Left and Right move the playback position. Shift moves five waveform bars. Seeking stops playback.
Space plays from the selected position or stops playback.
The marker follows player-reported progress. Players without progress reports keep a stationary marker.
With the result focused, S opens Save and H opens History.
Save exports the complete original audio, including when playback starts from a selected position.
The default destination is `~/Downloads/gpt-audio`, with a filename derived from the retained prompt.
Enter confirms the suggested destination. Tab edits Filename or Save to without moving values below their labels.
Enter on Save to finishes editing; it does not save. Default-name collisions receive numbered filenames.
History is available directly in the form and through More actions.
History shows prompt previews and generation metadata. Opening an entry restores its settings without generation.

Native WAV analysis supports PCM at 8, 16, 24, or 32 bits and floating-point samples at 32 or 64 bits.
API PCM also supports waveform analysis without external tools.
MP3, Opus, AAC, FLAC, and other WAV variants require FFmpeg on `PATH`.
Those WAV variants include compressed encodings, RF64, and streaming headers with unknown lengths.
Playback requires FFplay. Audio requiring external decoding also needs FFmpeg.
The CLI does not install either tool.
Missing tools do not delete retained audio or prevent exporting its original bytes.
The form supports Play, Stop, Replay, and seeking. It does not provide a separate Pause control.

The chooser includes the seven models, thirteen voices, and six formats shown in the supplied Playground screenshots.
The API determines model access and availability.
The `tts-1-1106` and `tts-1-hd-1106` snapshots appear in those screenshots; current public documentation does not confirm access.
The form rejects unsupported voices for the `tts-1` family before generation.
It keeps unsupported Instructions in the draft and omits them from requests.
Direct flags retain their existing API behavior, including custom voices and future model IDs.

As of October 9, 2026, four listed models have a January 6, 2027 shutdown date.
They are `tts-1`, `tts-1-hd`, `gpt-4o-mini-tts-2025-03-20`, and `gpt-4o-mini-tts-2025-12-15`.
The [deprecation notice](https://developers.openai.com/api/docs/deprecations#2026-10-01-text-to-speech-models) recommends a Realtime migration.
That replacement uses a different workflow. This form continues to use the Speech endpoint and preserves explicit model choices.

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
Roomy terminals show timestamps and selected-recording metadata.
Compact terminals preserve prompt space and keep the selected recording visible.
More actions keeps the draft visible above its list when space permits, as in the image picker.
Escape returns to the form; the menu omits a duplicate Back action.
Results show the default export folder below the playback and Save controls.
Only completed exports show a Saved receipt. Waveform columns use narrow strokes instead of filled blocks.
Roomy result views leave one blank row between the waveform and playback time.

## Layout

Settings labels and values use normal terminal text. Save labels retain secondary styling.
More actions reuses the form layout when space permits, keeping the draft and settings visible.
Compact screens prioritize the selected action and return guidance.
Roomy playback uses light waveform strokes and a distinct progress marker.
Play and Save share one row when they fit. The next row shows the suggested export folder.
After export, that row shows the actual saved path instead.
Filename and Save to keep their value columns while editing.

Wide terminals use a reading column of at most 96 cells.
Settings follows the composer. Instructions appears first within its aligned rows.
Generate, History and More actions share a row when they fit.
Left and Right move between these actions. Up and Down retain their existing behavior.
Compact terminals retain stacked controls. Keyboard keys use the accent color; descriptions use secondary text styling.
