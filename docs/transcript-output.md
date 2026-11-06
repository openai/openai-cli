# Transcript timestamps

Finite transcription and translation responses show the complete transcript first.
Each segment then groups its text, speaker, timestamps, and details together.
Readable output applies in terminals and pipes.
These examples use existing commands and flags.

```sh
openai audio transcribe --file meeting.wav --model gpt-4o-transcribe-diarize --response-format diarized_json --chunking-strategy auto
```

For example, the readable output includes:

```text
Thanks for calling. I need help.

Segments:
  1.
    Speaker: A
    Start: 00:00.000 (0 s)
    End: 00:05.200 (5.2 s)
    Text: Thanks for calling.

  2.
    Speaker: B
    Start: 00:05.200 (5.2 s)
    End: 00:12.800 (12.8 s)
    Text: I need help.
```

The API supplies the timestamps and speaker labels.
The CLI preserves segment order, including overlaps.
It rounds displayed timestamps to milliseconds and includes hours when needed.
Each timestamp includes its original numeric seconds, including sub-millisecond precision.
Segment IDs, confidence details, and unknown fields remain beside their segment's text and speaker.
Word timestamps, usage, and other response metadata remain below the transcript.
The API's field order determines each group's field order.

Missing speakers receive no invented label.
Missing, malformed, negative, reversed, or unusually represented timestamps retain their original values.
Malformed segment arrays retain the previous readable presentation.
Readable output escapes terminal controls without truncating long text.

The aggregate transcript remains available even when its words also appear in segments.
This repetition keeps the complete transcript readable while each segment retains its associated details.
Segment text preserves its original spaces, tabs, and newlines.

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
These transcript formatting rules do not change model defaults, request inputs, or streaming behavior.

The [speech-to-text guide](https://developers.openai.com/api/docs/guides/speech-to-text#speaker-diarization) documents these API options.
Diarization requires chunking for inputs longer than 30 seconds; `auto` selects server chunking.
The diarization model does not support `timestamp_granularities`.
As of October 9, 2026, these examples use the documented specialized models.
The [deprecation notice](https://developers.openai.com/api/docs/deprecations#2026-08-26-transcription-models) schedules both models for removal on February 26, 2027.
Synthetic CLI checks verify presentation and request construction, not live model access.
