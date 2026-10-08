# Compact list table evidence

This publication head does not activate tables through public API commands.
Generated command activation is excluded from this publication scope.
The recorder and both checkers require a separately activated candidate from normal generated promotion.
Do not run them against this publication head expecting table activation.
Historical active captures are integration evidence only, not this publication head's public behavior.
For this publication, explain that no public command output changes instead of presenting historical captures as current behavior.

The fixture serves synthetic files, batches, and projects on loopback only.
The recorder uses the shared `scripts/demos/capture_and_render.sh` lifecycle.
Each recording runs before and after binaries with identical responses and terminal settings.

After generated promotion, use separate binaries from the baseline and activated candidate checkouts.
Use the reviewed navigation dependency as the baseline when comparing table activation.
Do not replace the installed `openai` executable.
Use full source commit IDs in the recording command.
If a binary includes uncommitted changes, record that limitation beside its binary hash.

```sh
GOMAXPROCS=2 go build -p 2 -o /tmp/list-table-demo-api ./scripts/demos/list-tables
DEMO_API_BINARY=/tmp/list-table-demo-api \
  bash scripts/demos/list-tables/record.sh \
  /tmp/openai-before /tmp/openai-activated BEFORE_SHA ACTIVATED_SHA /tmp/list-tables-wide 110
DEMO_API_BINARY=/tmp/list-table-demo-api \
  bash scripts/demos/list-tables/record.sh \
  /tmp/openai-before /tmp/openai-activated BEFORE_SHA ACTIVATED_SHA /tmp/list-tables-narrow 40
```

Each output directory must be empty and outside the repository.
The commands require Go, Python 3.9 or later, asciinema, agg, ffmpeg, and ffprobe.
The recorder isolates the process environment and supplies clearly fake keys.

For an independently activated resource, append `--resource files` after the width.
The recorder then captures and validates only that resource.
Without this option, all three resources and their additional scenarios remain required.
Do not present a selected-resource recording as evidence for other resources.

Scenes run these existing commands:

```sh
openai files list
openai batches list
openai admin organization projects list
```

Each scene asserts exit status zero and exactly one API request.
The scene driver relays a child PTY through the shared recorder.
It disables outer PTY output translation while relaying CLI bytes.
It restores those settings before the recorder prints its prompt.
The validator compares recorded CLI bytes with the driver's SHA256 digest.
The recorder uses agg's swash renderer for incremental redraws.
It sends `q` only after loaded content and the final-page footer appear.
For dedicated print-page probes, the driver accepts `DEMO_EXIT_KEY=p` and an optional `DEMO_READY_MARKER`.
It sends `p` only when the loaded-page print hint and content marker appear.
The standard recorder continues to use `q`; it does not claim print-page coverage.
Paginated commands in the navigation baseline and candidate receive `q` after the loaded final-page footer appears.
The fixtures include long Unicode names and preserve complete IDs.
The validator compares pipe output across default text, explicit formats, extraction, and raw output.
It also compares explicit output modes with stdin and stdout connected to a PTY.
Checks include raw output alone, mixed-case formats, and explicit automatic format with extraction.
Additional pipe comparisons cover empty pages, controls, unfamiliar fields, errors, and item limits across all resources.
Paginated resources also cover two-page output, exact cursors, and partial failures.
Explicit-mode PTY checks use the same width as the recording.
The validator retains exact stdout, stderr, and exit statuses under `machine/`.

The public page checker also exercises automatic empty results, controls, unfamiliar fields, and API errors.
It uses the existing `image_picker_harness.Terminal` helper and retains terminal bytes, casts, and request logs.
For paginated resources, a 77-character ID forces a narrow viewport fallback.
The checker resizes the loaded page without fetching another page.
It presses `p` and checks complete logical ID lines after terminal restoration.
The viewport can wrap IDs; the printed page supplies complete copyable lines.

The outputs include terminal casts, GIFs, PNGs, transcripts, request logs, and tool metadata.
Inspect the PNGs before reporting visual success.
These captures show terminal replay, not native terminal appearance.
These checks do not establish native clipboard behavior or the complete navigation lifecycle.
