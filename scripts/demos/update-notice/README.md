# Update notice comparison

This recorder runs bare `openai` on real baseline and candidate binaries.
It records four scenes in isolated 80-column Bash PTYs:

1. Baseline with a newer release in its synthetic cache.
2. Candidate with the same newer release.
3. Candidate again with the same cache.
4. Candidate with its installed version in the cache.

Only the second scene shows the update notice.
The recorder compares full help content after PTY newline normalization.
The comparison collapses repeated CR immediately before LF; all other bytes must match.
Original casts and CLI `.tty` files retain every captured newline byte.
It also checks the persisted notice timestamp and requires zero API requests.
The synthetic release uses the next major version; it does not claim an actual available release.

Normal terminal scrolling shows the help footer in each screenshot.
The scene script prints a final shell comment to identify the scene.
The CLI output remains unchanged in the recording.
Full output remains available in the asciicast files and plain transcripts.
This evidence shows a terminal replay, not native graphical terminal or font validation.

Build the baseline from the exact comparison commit in a separate checkout.
Build the candidate from the reviewed candidate source.
Use the same stable runtime version for both binaries.
Record each source commit and binary hash with the evidence.
The recorder hashes binaries but cannot establish their source provenance.
If the source is uncommitted, supply its manifest through `DEMO_SOURCE_MANIFEST`.
The recorded source parent does not identify those uncommitted bytes alone.

Reuse the existing rejecting API fixture:

```sh
GOMAXPROCS=2 go build -p 2 -o /absolute/path/image-model-demo-api \
  ./scripts/demos/image-models/main.go
```

Acquire the required build and PTY resources before execution.
Keep the fixture binary's source identity with the evidence.
The recorder selects its rejecting route and supplies no API credentials.
Fresh private caches prevent GitHub release checks during these scenes.
Temporary HOME and XDG paths protect personal state.
The shared capture lifecycle cleans up its owned processes and temporary directories.

Run the recorder from the candidate checkout:

```sh
DEMO_API_BINARY=/absolute/path/image-model-demo-api \
  bash scripts/demos/record-update-notice.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/update-notice-demo
```

The recorder requires existing `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` commands on PATH.
Use a new, empty output directory outside the repository.
Preserve failed recording directories when correcting the recorder.
Record a new run in a separate directory after each correction.
The output includes `comparison.gif`, four PNG screenshots, transcripts, source hashes, cache snapshots, and validation results.
Keep generated media outside Git and attach the reviewed media to the PR.
