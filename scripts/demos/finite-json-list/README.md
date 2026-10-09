# Finite JSON list comparison

This recipe captures the same commands against one synthetic loopback API.
The API returns two files across two pages.
The `--purpose batch` query returns an empty list.
Each scene writes the actual CLI output to files and displays those files.
Python's standard JSON parser checks the complete bytes.

The baseline emits separate JSON values and no bytes for an empty list.
Its array parser checks must fail.
The candidate emits one JSON array, including `[]` for an empty list.
Both binaries preserve `--format jsonl` for one JSON value per line.
The validator compares all records and identical request/response sequences.
It also checks real process statuses and separate stderr files.

The recorder reuses `../capture_and_render.sh` for capture, rendering, cleanup, and assembly.
The Python fixture uses the standard library.
It rejects unexpected requests and credentials without logging headers.
Its single request loop has a two-second connection timeout and a bounded idle wait.
The shared recorder stops and reaps the fixture.
Each CLI scene uses a temporary home, an isolated environment, and a fake key.
Temporary executable links provide `openai`; the recorder installs nothing.

Use existing Bash, Python 3, asciinema, agg, ffmpeg, ffprobe, and the Menlo font.
Verify both CLI binaries against their source commits before recording.
The baseline is `e68939820415144d769ed02de6aa72d5b7d32948`.
Obtain the coordinator's source-build reservation before building either binary.
Obtain the coordinator's PTY reservation before recording.
Use a new output directory outside the repository.

```sh
PATH=/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH \
  bash scripts/demos/finite-json-list/record.sh \
    /path/to/evidence/bin/openai-before /path/to/evidence/bin/openai-after \
    e68939820415144d769ed02de6aa72d5b7d32948 "$AFTER_SHA" \
    /path/to/evidence/demo/finite-json-list
```

The recorder retains casts, GIFs, screenshots, transcripts, raw output, stderr, statuses, request hashes, and tool metadata.
Inspect both screenshots and representative GIF frames before sharing.
Check command visibility, array brackets, parser results, and JSONL migration text.
Keep all binary media outside Git.

These recordings are terminal replays using synthetic responses.
They do not establish native graphical-terminal appearance, Windows execution, or live API behavior.
The feature tests separately cover incremental output, cancellation, malformed records, and write failures.
After those failures, callers must retain the nonzero status and treat incomplete output as incomplete JSON.
