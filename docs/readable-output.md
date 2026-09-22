# Reading command results

The CLI prints readable output by default, both in a terminal and when piped or
redirected. `--format auto` and `--format text` select this behavior explicitly.
Lists and streams print directly without opening an automatic pager.

```sh
openai models list
openai responses create --model gpt-5.5 --input "Explain a rainbow in one sentence"
```

Known text responses, chat completions, and audio transcripts show their text.
Requested timestamps, speaker segments, and log probabilities remain visible
as labeled details. Streamed text appears as it arrives; completion usage appears
separately. List items retain their IDs and key fields. Other responses show labeled fields,
nested details, and separate list entries, for example:

```text
ID: file-example
Filename: notes.txt
Bytes: 120
Purpose: assistants
```

Normal text is kept in full. Known base64 image/audio fields and numeric embedding
vectors show their size or count with a `--format json` hint. Terminal control
and directional characters are escaped in readable output, including redirected
text. Unknown response shapes still show their fields.

## Short resource summaries

Models, files, batches, vector stores, fine-tuning jobs, assistants, and thread
runs show their ID and the fields needed for the next step: status, filenames,
result IDs, counts, or required actions. For example, a completed batch shows
its output file ID and request counts without repeating every lifecycle timestamp.
Confirmed deletions say what was deleted and include its ID.

Errors, failed counts, warnings, and required tool calls stay visible. Summaries
include `Use --format json for all fields.` when configuration or metadata is
left out. `--format json` returns the complete original API value, including
instructions, timestamps, and configuration. New server fields fall back to
complete labeled output so new results are not silently hidden.

## Scripts and complete API data

**The default has changed: redirecting stdout no longer selects JSON.** Update
scripts that parse API responses to request their format explicitly:

```sh
openai --format json models list > models.json
openai --format json files list | jq '.id'
```

`--format json` includes full API data, including base64 content and embedding
vectors. Explicit `json`, `jsonl`, `raw`, `yaml`, `pretty`, and `explore` retain
their existing behavior; choose `explore` when you want the interactive viewer.
`--transform` and `--raw-output` also retain their existing data extraction
behavior. Readable output is for reading; use an explicit data format for parsing.

Binary downloads keep their existing byte output or file behavior. Successful
deletions and call/session actions with an empty API response print a short
confirmation in readable mode. Explicit data formats keep the empty response.

## Audio and exact bytes

Transcription and translation text, SRT, and VTT responses retain their full
content. Readable output escapes terminal controls; `--format json` represents
the returned text as a JSON string. Use `--format raw` or `--raw-output` for the
original text bytes, for example when saving subtitles:

```sh
openai --format raw audio:transcriptions create --file speech.wav \
  --model whisper-1 --response-format srt > subtitles.srt
```

Speech audio downloads keep their existing binary output and file behavior.
With `--stream-format sse`, speech prints readable events when `--output` is
omitted. Use `--format json` or `jsonl` for full event data, or `--format raw`
or `--raw-output` alone for the original SSE bytes. `--transform` selects event
fields; combine it with `--raw-output` to extract unquoted strings.

An explicit speech `--output FILE`, `--output -`, or `--output /dev/stdout`
keeps the original response bytes, including SSE framing when selected. SSE
failure events still return a nonzero exit code; any received bytes are kept.
For an ordinary audio file:

```sh
openai audio:speech create --model gpt-4o-mini-tts --voice alloy \
  --input "Hello" --output speech.mp3
```

## Images

Image generation, edits, and variations save files automatically, including when stdout is redirected:

```sh
openai images generate --prompt "A tiny orange robot" > saved-paths.txt
openai images edit --image robot.png --prompt "Give the robot a blue hat"
openai images create-variation --image robot.png
```

The default folder is `~/Downloads/gpt-images/`. Redirected output contains
readable saved-file information; previews require a supported terminal. To
receive the complete API response without saving images:

```sh
openai --format json images generate --model gpt-image-2.5-sunburst \
  --prompt "A tiny orange robot" > image-response.json
```

Original input files are kept. Edits use the default image model; variations use
DALL-E 2. See the [image guide](image-output.md) for progress streams, file names,
model compatibility, and preview support.

## Errors

Errors go to stderr and use readable text by default. `--format json` also
selects JSON errors. `--format-error` overrides the error format independently:

```sh
openai --format json models list > models.json 2> error.json
openai --format-error json models list
```

If an HTTP failure has no usable JSON error body, JSON error output contains
`status_code` and a local `message` explaining the failure. Valid API error
payloads keep their original fields. Errors remain on stderr with a nonzero exit.

When the API identifies an invalid argument, the message points to the matching
command option and its help. Known choices are shown when available. Server
messages and rejected values are available through the explicit JSON error
format rather than copied into the default guidance.

Recognized failed, cancelled, expired, or incomplete stream events return a
nonzero exit code, including with explicit JSON or field extraction. Events
already received remain in the output so callers can inspect the failure.
Check the exit code before parsing an output file.
