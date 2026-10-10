# Transcript timestamps

Finite transcription and translation responses show each text segment on its own line.
Readable output applies in terminals and pipes.
These examples use existing commands and flags.

```sh
openai audio transcribe --file meeting.wav --model gpt-4o-transcribe-diarize --response-format diarized_json --chunking-strategy auto
```

For example, matching aggregate text and segments produce:

```text
[00:00.000–00:05.200] A: Thanks for calling.
[00:05.200–00:12.800] B: I need help.
```

The API supplies the timestamps and speaker labels.
The CLI preserves segment order, including overlaps.
It rounds displayed timestamps to milliseconds and includes hours when needed.
Sub-millisecond values remain in metadata.
Unknown fields, segment IDs, word timestamps, confidence details, and usage remain available below the transcript.

Missing speakers receive no invented label.
Missing, malformed, reversed, or unusually represented timestamps remain in metadata without a formatted range.
Malformed segment arrays retain the previous readable presentation.
Readable output escapes terminal controls without truncating long text.

Matching segments replace the full transcript to avoid repetition.
The comparison ignores spaces, tabs, and newlines at segment boundaries.
If the full transcript differs, the CLI preserves it before the labeled segments.
This includes aggregate text with speaker annotations or additional words.

```sh
# Verbose segment timestamps use the existing timestamp option.
openai audio transcribe --file meeting.wav --model whisper-1 --response-format verbose_json --timestamp-granularity segment

# Translation uses the same finite presentation when the API returns segments.
openai audio translate --file meeting.wav --model whisper-1 --response-format verbose_json

# Explicit data formats retain the complete original response.
openai --format json audio transcribe --file meeting.wav --model gpt-4o-transcribe-diarize --response-format diarized_json --chunking-strategy auto

# Extraction retains the aggregate transcript.
openai --transform text --raw-output audio transcribe --file meeting.wav --model whisper-1

# Raw output retains subtitle bytes, including CRLF line endings.
openai --format raw audio transcribe --file meeting.wav --model whisper-1 --response-format srt > captions.srt
```

JSON, JSONL, YAML, pretty, explore, raw output, and extraction keep their existing behavior.
Plain text, SRT, VTT, and streaming retain the [existing audio presentation](readable-audio.md).
This feature changes no model defaults, request inputs, audio capture, playback, or Live behavior.

The [speech-to-text guide](https://developers.openai.com/api/docs/guides/speech-to-text#speaker-diarization) documents these API options.
Diarization requires chunking for inputs longer than 30 seconds; `auto` selects server chunking.
The diarization model does not support `timestamp_granularities`.
As of October 9, 2026, these examples use the documented specialized models.
The [deprecation notice](https://developers.openai.com/api/docs/deprecations#2026-08-26-transcription-models) schedules both models for removal on February 26, 2027.
Synthetic CLI checks verify presentation and request construction, not live model access.
