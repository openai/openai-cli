# Schema helper demo

This demo serves a fixed invoice schema through a loopback Responses fixture.
It sends no live API requests and makes no live model acceptance claim.
The candidate must compile the schema before saving the exact response bytes.
The receipt distinguishes local compilation from unchecked Structured Outputs compatibility.

Build the fixture from the repository root:

```sh
go build -o /tmp/schema-helper-demo-api ./scripts/demos/schema-helper
```

Build the candidate CLI separately from its reviewed commit.
Put `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` on `PATH`.

Record matching commands with pinned binaries and commits:

```sh
DEMO_API_BINARY=/tmp/schema-helper-demo-api \
  bash scripts/demos/schema-helper/record.sh \
  /absolute/path/to/before/openai /absolute/path/to/after/openai \
  BEFORE_COMMIT_SHA AFTER_COMMIT_SHA /absolute/path/to/empty-output-directory
```

The baseline command must fail without saving a file.
The default expected baseline status is `1`.
Set `DEMO_BEFORE_STATUS` only when a verified baseline reports another failure status.
The candidate must exit `0`, save unchanged bytes, and print the exact receipt.
The fixture must record exactly one request with JSON mode, `store=false`, and the explicit model.

The shared recorder creates isolated homes and separate working directories.
It preserves captures, request checks, statuses, and schema artifacts outside the repository.
Set `DEMO_SOURCE_MANIFEST` to retain an existing source manifest with the recording.
The recorder logs binary hashes; the caller must establish their source provenance.

Inspect `before.png`, `after.png`, and `comparison.gif` before sharing.
The 80-column, 24-row captures show terminal replay, not native terminal validation.
Keep recorded media outside Git.
