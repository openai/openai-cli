# Custom voice help comparison

This comparison runs help only. It creates no voice and uploads no recording.
The local fixture rejects and records unexpected API requests.
The recorder requires zero requests.

## Prepare

Build one CLI from the comparison base and another from this change.
Keep both binaries outside the repository.
Record each source commit and binary SHA256.

Build the existing synthetic fixture from this checkout:

```sh
go build -o /tmp/custom-voice-help-api ./scripts/demos/image-models
```

The shared recorder requires `asciinema`, `agg`, `ffmpeg`, `ffprobe`, Bash, and the Menlo font.
Use a fresh output directory outside the repository for each recording.

## Record

Set `BEFORE_BINARY`, `AFTER_BINARY`, `BASE_SHA`, `HEAD_SHA`, and `OUTPUT_DIR` to your prepared paths and exact commits.

```sh
DEMO_API_BINARY=/tmp/custom-voice-help-api DEMO_ROWS=42 \
  scripts/demos/record-help.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" "$BASE_SHA" "$HEAD_SHA" "$OUTPUT_DIR" audio
```

The Before view advertises description-based voice creation.
The After view describes an audio sample and consent.
The recorder saves raw terminal captures, transcripts, screenshots, a comparison GIF, and execution metadata.
These are terminal replays, not native graphical-terminal validation.

To inspect the added setup guidance separately:

```sh
openai audio voices create --help
openai help audio:voices create
```

Both spellings show the consent prerequisite and one complete sample-based example.
The example requires an uploaded consent ID and an approved sample before live use.
Synthetic test success does not establish consent validity or account eligibility.

## Cleanup

The recorder stops its fixture and removes its temporary capture directory automatically.
Keep the output directory for review.
Remove the temporary fixture binary after recording.
Attach reviewed GIFs and screenshots to the PR; do not commit binary media.
