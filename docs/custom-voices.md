# Create a custom voice

Custom voices require an eligible account, a consent recording, and a matching audio sample from the same speaker.
Text descriptions cannot replace these recordings.
See the [custom voices guide](https://developers.openai.com/api/docs/guides/custom-voices) for eligibility and current recording requirements.

## Prepare consent

Record the exact consent phrase from the official guide.
Upload it through the [voice consent API](https://developers.openai.com/api/reference/resources/audio/subresources/voice_consents/methods/create).
Consent creation requires `name`, `language`, and the `recording` file.
Keep the returned consent ID for voice creation.

This CLI does not currently provide `audio voice-consents` commands.
The HTTP API supports consent creation, listing, retrieval, label updates, and deletion.
Consent management and voice management are separate API resources.

## Create the voice

Set `OPENAI_API_KEY` through your normal secure environment setup.
Replace `cons_demo` with your uploaded consent ID and `sample.wav` with your matching sample recording.

```sh
openai audio voices create --name Demo --consent cons_demo --audio-sample sample.wav
```

The command sends a multipart request and returns the saved voice metadata.
The existing `audio:voices create` spelling also works.
The optional `--type` defaults to `audio_sample`.
The command does not support `--prompt` or creation from a text description.

Samples can use MPEG, WAV, OGG, AAC, FLAC, WebM, or MP4 audio.
The API currently limits samples to 10 MiB and 30 seconds.
See the [voice API reference](https://developers.openai.com/api/reference/resources/audio/subresources/voices/methods/create) for supported MIME types.

## Use in scripts

Explicit output flags keep their existing behavior.
Use `--format json` for the complete response, including its voice ID.
JSONL, YAML, and raw JSON also remain available.

```sh
openai audio voices create --name Demo --consent cons_demo --audio-sample sample.wav --format json
```

Use `--transform id --raw-output` to print only the returned voice ID.
`--header`, `--organization`, `--project`, and `--base-url` retain their existing request behavior.
Only run creation examples when you intend to upload a recording and create a voice.

## Access and errors

An accepted synthetic request does not prove consent validity, speaker matching, or account eligibility.
If creation fails, inspect the API error before retrying.
Check custom-voice access, the consent ID, and the recording requirements from the official guide.
Use `--format-error json` when a script needs the complete API error.

The public voice API documented here exposes creation.
This CLI does not provide voice listing, retrieval, renaming, deletion, or reference previews.
The consent API's operations do not establish those voice operations.
