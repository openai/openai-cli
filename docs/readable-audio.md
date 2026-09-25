# Reading audio results

Transcription and translation results print the transcript first. Metadata,
including language, duration, timestamps, speaker segments, log probabilities,
usage and unfamiliar fields, remains below it. Readable output applies in pipes
as well as terminals.

```sh
openai audio:transcriptions create --file sample.wav --model gpt-4o-transcribe
openai audio:transcriptions create --file sample.wav --model gpt-4o-transcribe --stream=true
openai audio:translations create --file sample.wav --model whisper-1
openai --format json audio:transcriptions create --file sample.wav --model whisper-1
```

Streamed transcript deltas appear immediately. A matching final text snapshot
does not repeat the transcript. Revised final text is labeled as an update.
Diarized events retain segment IDs, speakers and timestamps; a final aggregate
may repeat the segment text because it is a separate representation.

The existing `--response-format text`, `srt` and `vtt` responses keep their text
instead of being interpreted as JSON. Readable output escapes terminal controls
and adds a final newline when needed. Use `--format raw` to retain the exact
subtitle bytes, including CRLF line endings. JSON formats encode native text as
a JSON string.

```sh
openai --format raw audio:transcriptions create --file sample.wav --model whisper-1 --response-format srt > captions.srt
```

Speech SSE shows an audio-size summary and retains usage and other metadata.
No audio is played automatically. Explicit JSON, JSONL, YAML, pretty and explore
formats retain event payloads; `--transform` retains extraction behavior.
`--format raw`, `--raw-output` without extraction, and `--output` retain the
original SSE bytes. Binary speech and other downloads keep their existing
stdout and file behavior regardless of the presentation format.

```sh
openai audio:speech create --model gpt-4o-mini-tts --voice coral --input 'Hello' --stream-format sse
openai --format jsonl audio:speech create --model gpt-4o-mini-tts --voice coral --input 'Hello' --stream-format sse
openai audio:speech create --model gpt-4o-mini-tts --voice coral --input 'Hello' --output speech.mp3
```

Failures and interrupted streams retain already written output and exit
unsuccessfully. A stream that starts recognized audio events but ends before
its final event is incomplete. Unknown events remain visible. Raw speech SSE
is checked for failures while its bytes are copied; a saved partial file is
not reported as a successful download.

These examples use existing commands and flags. Model and response-format
support still depends on the API. There are no image features or new credentials.
