# Compact list table evidence

Files, batches, and organization projects still require public activation through normal generated promotion.
This handwritten change does not activate those three command paths.
Models already activates its separate viewer and does not need those generated adapters.
This recipe covers only files, batches, and organization projects.
The recorder and both checkers require an activated candidate for the selected resources.
Historical active captures validate integration and tooling, not this publication head's three-resource behavior.

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
Complete first results that fit on one screen print once and exit without a key.
The fit calculation reserves one row for the shell prompt and accounts for Unicode widths and tabs.
Empty successful results print `No results.` and exit when that message fits.
Longer results retain navigation, even when the API returns only one page.
Incomplete API pages remain interactive and fetch another page only after user input.
The footer shows `b: back` when space permits.
The footer shows `End of results` only at the bottom of the final page.
The recorder sends `q` only when loaded content and the final-page footer remain visible.
For dedicated print-page probes, the driver accepts `DEMO_EXIT_KEY=p` and an optional `DEMO_READY_MARKER`.
It sends `p` only when the loaded-page print hint and content marker appear.
The standard recorder allows automatic exit or sends `q`; it does not claim print-page coverage.
The fixtures include long Unicode names and preserve complete IDs.
The validator compares pipe output across default text, explicit formats, extraction, and raw output.
It also compares explicit output modes with stdin and stdout connected to a PTY.
Checks include raw output alone, mixed-case formats, and explicit automatic format with extraction.
Additional pipe comparisons cover empty pages, controls, unfamiliar fields, errors, and item limits across all resources.
Paginated resources also cover two-page output, exact cursors, and partial failures.
Explicit-mode PTY checks use the same width as the recording.
The validator retains exact stdout, stderr, and exit statuses under `machine/`.

The public page checker also exercises automatic empty results, controls, unfamiliar fields, and API errors.
It reuses the terminal harness and navigation checker's completion, footer, row-counting, and terminal-restoration helpers.
It retains terminal bytes, casts, key events, final printed bytes, and request logs.
Complete-result checks send no keys and reject missing or duplicated IDs.
Controls and unfamiliar-field checks also repeat at a height that requires navigation.
The unfamiliar-field check scrolls to its final detail before printing.
For paginated resources, a 77-character ID forces a narrow viewport fallback.
The checker resizes the loaded page without fetching another page.
It presses `p` and checks complete logical ID lines after terminal restoration.
The viewport can wrap IDs; the printed page supplies complete copyable lines.
Each checker case requires exactly one API request and the expected exit status.

The outputs include terminal casts, GIFs, PNGs, transcripts, request logs, and tool metadata.
Inspect the PNGs before reporting visual success.
These captures show terminal replay, not native terminal appearance.
These checks do not establish native clipboard behavior or the complete navigation lifecycle.
