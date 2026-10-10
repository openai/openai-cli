# Stored-completion export demo

This recipe compares two real CLI binaries against a synthetic loopback API.
The baseline already lists every page with explicit JSONL output.
The candidate adds an explicit destination, safe file publication, and a completion receipt.

## Record

Provide verified baseline and candidate binaries, their full commit IDs, and an empty output directory outside the repository.
Install Python 3, asciinema, agg, ffmpeg, and ffprobe on PATH.
The renderer uses the Menlo font.
The fixture uses only Python's standard library.

```sh
bash scripts/demos/stored-completion-export/record.sh \
  /path/to/before/openai /path/to/after/openai \
  BEFORE_COMMIT AFTER_COMMIT /path/outside/repository/stored-export-demo
```

For uncommitted development builds, set `DEMO_AFTER_SOURCE_STATE` and `DEMO_SOURCE_MANIFEST` to identify the exact candidate source.
The recorder verifies binary and script hashes before and after capture.
It uses the shared `capture_and_render.sh` lifecycle for processes, capture, rendering, and cleanup.
It neither builds nor installs the CLI.

## Scenes

1. Before: run `openai chat completions list --format jsonl --max-items -1 --limit 2 > completions.jsonl`.
2. After: run `openai chat completions export --limit 2 --output completions.jsonl`.
3. Existing file: verify the export rejects an existing destination before requesting data.
4. Empty result: verify the export saves an empty file and reports zero records.

The fixture returns three completions across two pages.
Every completion contains synthetic choices, metadata, and an unknown field with an exact large integer.
The recorder checks exact saved bytes, page requests, statuses, receipts, and temporary-file cleanup.
The existing-file scene returns status 1 and preserves its original bytes.

## Evidence

The output includes `comparison.gif`, labeled `before.png` and `after.png`, and existing-file and empty-result screenshots.
It also retains raw casts, transcripts, saved JSONL files, fixture requests, source hashes, binary hashes, and validation results.
All recorded media stays outside Git.

Inspect the GIF and screenshots for clipping, readability, correct behavior, and sensitive data before sharing.
The capture uses isolated Bash sessions, fake credentials, and separate temporary homes.
The CLI inherits terminal stderr; the baseline redirects stdout to a file.
The 92×20 terminal replay uses Menlo at 20 pixels with the asciinema theme.
This demonstrates local CLI execution, not live API acceptance or native graphical terminal appearance.

The shared recorder removes its temporary homes and stops only its own processes.
The output directory retains the review evidence until its owner removes it.
