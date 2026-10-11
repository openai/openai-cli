# Speech controls and retained audio

Run this command with terminal input and output:

```sh
openai audio speak
```

The form exposes text, instructions, model, voice, response format, and speed.
Tab moves between editors, settings, the command preview, and the result.
Up and Down select settings. Enter opens a choice or starts generation from an editor.
Enter on Speed accepts an exact number from 0.25 to 4. Left and Right adjust speed by 0.25.
Alt+Enter adds a newline. Ctrl+G generates from any ordinary form section.
The form follows the image picker’s bordered editor, aligned settings, selection colors, and muted command preview.
The focused command preview supports Up/Down scrolling and Left/Right adjustments.
The preview uses Bash/zsh syntax and preserves supported project, organization, and endpoint overrides.
Private credential overrides suppress the preview instead of exposing their values.
The editors normalize line endings, expand tabs, and remove unsupported control and replacement characters.
Use direct flags or file inputs to preserve text that the editors normalize.
Existing `@file` expansion and escaped `\@` literal input rules still apply to direct commands.

Choosing a model or voice does not generate speech.
Preview voice explicitly generates and plays a short sample using the selected settings.
An identical retained sample can play again without another API request.
With the audition result focused, Esc restores a previous result if one exists, then returns to Text.
Normal generation also requires an explicit action.

The form retains completed audio, text, instructions, and settings in private local history.
It stores history under the operating system's user cache directory, within `openai/audio`.
History can contain sensitive text. The form states what it stores before generation.
Clear resets the form and current result. It preserves history and exported files.

Completed results show their format and byte count.
When analysis succeeds, they also show a waveform derived from decoded samples and duration.
Result labels retain the original generation settings when you change the form.
With the result focused, Space plays or stops it. Playback always starts from the beginning.
With the result focused, S saves its original audio bytes to your chosen path.
History reopens a retained result and restores its settings without generation.

Native WAV analysis supports PCM at 8, 16, 24, or 32 bits and floating-point samples at 32 or 64 bits.
API PCM also supports waveform analysis without external tools.
MP3, Opus, AAC, FLAC, and other WAV variants require FFmpeg on `PATH`.
Those WAV variants include compressed encodings, RF64, and streaming headers with unknown lengths.
Playback requires FFplay. Audio requiring external decoding also needs FFmpeg.
The CLI does not install either tool.
Missing tools do not delete retained audio or prevent exporting its original bytes.
The form supports Play, Stop, and Replay. It does not provide pause or seek controls.

The chooser includes the seven models, thirteen voices, and six formats shown in the supplied Playground screenshots.
The API determines model access and availability.
The `tts-1-1106` and `tts-1-hd-1106` snapshots appear in those screenshots; current public documentation does not confirm access.
The form rejects unsupported voice and instructions combinations for the `tts-1` family before generation.
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
