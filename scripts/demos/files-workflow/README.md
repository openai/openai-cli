# Files workflow recording

This recorder compares real CLI binaries against one synthetic loopback API.
It uses the shared `capture_and_render.sh` lifecycle.
It creates temporary PATH entries named `openai`; it never changes the installed launcher.

Build the fixture from the repository root:

```sh
GOMAXPROCS=2 go build -p 2 -o /absolute/task/evidence/files-workflow-demo-api ./scripts/demos/files-workflow
```

Build the before and after binaries independently from their stated commits.
Supply full commit IDs and an empty output directory outside the repository.
Put `asciinema`, `agg`, `ffmpeg`, and `ffprobe` on PATH.
Set `GOCACHE` to a writable cache when required.

```sh
DEMO_API_BINARY=/absolute/task/evidence/files-workflow-demo-api \
  bash scripts/demos/files-workflow/record.sh \
  /absolute/task/evidence/baseline-openai \
  /absolute/task/evidence/candidate-openai \
  BEFORE_COMMIT AFTER_COMMIT \
  /absolute/task/evidence/files-workflow-media
```

For development captures, set `DEMO_SOURCE_MANIFEST` to the current source manifest.
The recorder preserves the manifest and both binaries' build information.
Final review media must identify the actual candidate source.

The before scene uses `create`, `retrieve`, and `content`.
The after scene uses positional `upload`, `get`, and `download`.
Both scenes upload the same 13-byte file with an explicit `user_data` purpose.
Both scenes inspect metadata and download text and binary files.
Each scene downloads through an explicit destination and shell redirection.
Actual `cmp` commands verify every downloaded byte.

The fixture records each request method, path, and content type.
It validates and records the exact synthetic upload filename, purpose, and bytes.
It rejects unexpected routes, credentials, upload fields, and upload bytes.
It checks response writes, request-body closes, log writes, and shutdown errors.
No live Files API acceptance claim follows from these synthetic requests.

The recorder preserves request logs, exact files, transcripts, casts, GIFs, PNGs, and binary hashes.
The validator requires six requests per scene and verifies eight downloaded files.
Any command failure stops its scene and fails capture.
The recorder checks fixture shutdown before validation.

Inspect `before.png`, `after.png`, and `comparison.gif` before sharing.
Check clipping, timestamp clarity, the complete ID, and copyable suggested commands.
The replay uses Bash, 110 columns, 46 rows, and Menlo at 18 pixels.
This is PTY execution with asciinema/agg replay, not native graphical terminal validation.
Keep generated media and executables outside Git.
