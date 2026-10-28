# Streaming text comparison

This recipe runs real before/after CLI binaries against the same four delayed
synthetic Responses events. Two text deltas arrive 650 ms apart, followed by a
matching text-done snapshot and a completed response containing the same text
and token usage. The after scene should print the text once while retaining
usage. The explicit JSONL scene must retain every original event.

The recorder uses asciinema 3.2.1, agg 1.9.0, ffmpeg, ffprobe and Python 3. Put
those tools on `PATH`. Its output is a terminal replay, not a native Apple
Terminal, PowerShell or cmd.exe capture. All scenes use a real PTY with an
isolated environment, a fake key, 90 columns by 40 rows, Menlo 22px, Dracula
colors and identical synthetic requests. The fixture listens only on loopback
and rejects other routes, request bodies and credentials.

Build the fixture from the repository root with the same pinned Go environment
used for the CLI. Store the executable and recordings outside Git:

```sh
go build -o /path/to/evidence/bin/streaming-demo-api ./scripts/demos/streaming-text
```

Build the before and after binaries from their identified commits. Verify that
the binaries match those revisions before recording; commit labels and recorded
hashes alone do not establish that correspondence. Then use an empty output
directory outside the repository:

```sh
DEMO_API_BINARY=/path/to/evidence/bin/streaming-demo-api \
  bash scripts/demos/streaming-text/record.sh \
    /path/to/before/openai /path/to/after/openai \
    "$BEFORE_SHA" "$AFTER_SHA" /path/to/evidence/demo
```

The runner records and checks all three exit statuses, waits for successful
fixture shutdown and request-log close, verifies identical
request contents and counts, compares explicit JSONL events with `events.json`,
checks text deduplication and usage, and verifies from cast timestamps that the
after scene displays each text delta before completion. It preserves individual
and combined casts, text transcripts, screenshots, GIFs, the fixture, commit
labels, binary and source hashes, tool versions, media dimensions and timings.
Each scene is rendered separately, then ffmpeg joins the original frames and
timing to avoid agg artifacts across screen clears. Rendering uses agg's resvg
backend and the larger text size so command spaces and flag hyphens stay
legible in the displayed comparison.

`../capture_and_render.sh` owns setup, fixture lifecycle, PTY capture, rendering
and assembly. The streaming scene, SSE fixture, tool pins and `validate.py`
checks stay here. Failures stop recording; exit and interruption clean up the
fixture and temporary state.

Inspect `before.png`, `after.png`, `explicit-jsonl.png` and representative GIF
frames before sharing. Check clipping, readable text, pauses, actual streaming,
matching commands and sensitive data. This demo illustrates the happy path;
tests provide the separate cancellation, malformed event and failure evidence.
Do not upload media until publication is authorized.

`streaming-text.tape` is an alternative VHS recipe, not the renderer used by
`record.sh`. To use it, start the fixture, provide `OPENAI_BASE_URL`, the fake
`OPENAI_API_KEY=synthetic-demo-key`, and `DEMO_BEFORE_DIR`/`DEMO_AFTER_DIR`
containing binaries named `openai`. Run VHS from an output directory outside
Git and validate that separate capture before describing it as VHS output.
