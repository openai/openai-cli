# Fine-tuning monitoring demo

Use immutable baseline and candidate executables. Build each from its recorded commit.
The recorder uses the shared `capture_and_render.sh` lifecycle with `asciinema`, `agg`, and `ffmpeg` on PATH.
Python 3 serves two synthetic responses on loopback. No live API credentials or training requests are used.

```sh
scripts/demos/fine-tuning/record.sh \
  /absolute/path/to/before/openai /absolute/path/to/after/openai \
  BEFORE_FULL_SHA AFTER_FULL_SHA /absolute/path/to/new-evidence-directory
```

Run from the repository root. The evidence directory must be empty and outside the repository.
Use `DEMO_WIDTH=40` for a narrow recording and `DEMO_THEME=github` for a light replay.
The default replay uses 80 columns and the Dracula theme. Both use `NO_COLOR=1`.

The first request returns an empty jobs page. The second returns HTTP 403 and must exit 1.
The candidate explains the successful empty result and preserves the denied-request failure.
The recorder checks process statuses, response counts, and captured text before assembling media.
The shared lifecycle stops its fixture server and removes its temporary homes after completion or interruption.

Retain casts, transcripts, request logs, hashes, GIF, and labeled before/after screenshots outside Git.
These are terminal replays, not native graphical-terminal or live account evidence.
