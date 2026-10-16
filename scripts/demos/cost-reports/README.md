# Project cost-report demo

This recipe runs the same command against the baseline and candidate binaries.
The baseline lacks `costs report` and exits with status 1.
The candidate reads two synthetic Costs API pages and exits with status 0.
The resulting totals are `proj_alpha usd 12.34` and `proj_beta usd 4.56`.
Both scenes use explicit dates, UTC, and project grouping.

The recorder reuses `../capture_and_render.sh` for capture, rendering, cleanup, and assembly.
The Python fixture serves loopback requests and checks query parameters and fake Admin credentials.
The fixture preserves request and response hashes without logging credentials.
SIGINT and SIGTERM stop the fixture, close its listener, and join its worker.
No dependency installation or Go build occurs during recording.

Obtain the coordinator's PTY reservation before running the recipe.
Use existing `python3`, `asciinema`, `agg`, `ffmpeg`, and `ffprobe` installations.
Use an empty output directory outside the repository.
Supply full source commit IDs matching both binaries.
Use a baseline binary from this commit:

```text
e68939820415144d769ed02de6aa72d5b7d32948
```

```sh
PATH=/absolute/terminal-replay-tools:$PATH \
  bash scripts/demos/cost-reports/record.sh \
  /absolute/task/evidence/baseline-openai \
  /absolute/task/evidence/candidate-openai \
  e68939820415144d769ed02de6aa72d5b7d32948 AFTER_COMMIT \
  /absolute/task/evidence/cost-reports-media
```

Set `DEMO_AFTER_SOURCE_STATE` when the candidate contains uncommitted changes.
Set `DEMO_SOURCE_MANIFEST` to preserve a matching source manifest.
The recorder checks binary and runtime source hashes before and after capture.
It requires exactly two candidate requests and no baseline requests.
It also validates command exit statuses and exact report totals.

The replay uses isolated Bash sessions, 110 columns, 22 rows, and Menlo at 18 pixels.
The shared capture environment supplies fake credentials and temporary homes.
Outputs include casts, transcripts, PNG screenshots, GIFs, request logs, status logs, and metadata.
Inspect `before.png`, `after.png`, and `comparison.gif` before sharing them.
Terminal captures combine stdout and stderr; public tests verify separate streams.
These captures establish terminal replay behavior, not native graphical appearance or live account acceptance.
Keep generated media and executables outside Git.

## Snapshot comparison

For a two-frame comparison, assemble the clean labeled screenshots with Python and Pillow:

```sh
python3 scripts/demos/cost-reports/assemble_snapshot_gif.py \
  /absolute/task/evidence/cost-reports-media/before.png \
  /absolute/task/evidence/cost-reports-media/after.png \
  /absolute/task/evidence/cost-reports-media/comparison-snapshots.gif
```

The output path must be new. The script verifies every composited GIF frame against its source screenshot.
It rejects unequal dimensions or more than 256 combined colors instead of silently approximating them.
Label this artifact as static snapshots from a terminal replay, with both original source commits.
It is not a new recording of later changes. Retain any earlier defective media alongside its review findings.
