# Automatic list pager evidence

Eligible automatic terminal lists use the pager without a mode flag.
Space advances through loaded chunks before requesting another API page.
Resource pagers omit the optional JSON-format hint.
This prevents a hint-only Space step when all loaded rows already fit.
`b` returns to loaded chunks without another request.
`q` restores the original terminal screen.
Short, empty, and final pages remain open until the user quits.
The pager ignores `i` and `p`.
An API failure preserves received records after restoring the terminal.
Explicit formats, redirected output, CI, and dumb terminals retain their existing paths.

The fixture serves synthetic Files, Batches, Projects, and Models data on loopback.
The recording compares the verified inline default with the pager default.
Both recordings run `openai files list --limit 1 --max-items -1` against two API pages.
Before sends Space and exits automatically.
After sends Space, `b`, Space, and `q`.
Both recordings require exactly two API requests.

Use the prior verified binary and the candidate binary.
Pass their source commit identities and an empty output directory outside the repository.
Record uncommitted source changes beside the binary hashes.

```sh
bash scripts/demos/list-pager/record.sh \
  /absolute/path/to/openai-before BEFORE_COMMIT \
  /absolute/path/to/openai-after AFTER_COMMIT \
  /absolute/path/to/empty-output
```

The recipe uses the shared capture lifecycle, Python 3, asciinema, agg, ffmpeg, and ffprobe.
It isolates credentials and configuration.
It preserves Apple Terminal identification for the terminal-mode compatibility fix.
It captures actual PTY bytes without rewriting CLI output.
It verifies exact relay hashes, blue headers, key events, requests, and restored history.
`after-first.png` shows the first page.
`after-final-page.png` shows the final page and its back control.
`after.png` shows the restored shell after quitting.
`comparison.gif` shows both complete interactions.
`validation.json` records the assertions.

Run the focused public observer with an empty output directory:

```sh
python3 -I -B scripts/demos/list-pager/observe.py \
  /absolute/path/to/openai-after /absolute/path/to/empty-checks \
  --baseline /absolute/path/to/openai-before \
  --asciinema /absolute/path/to/asciinema
```

The observer checks lazy requests, cached back navigation, final pages, narrow widths, resizing, cancellation, errors, and terminal restoration.
It also checks Models, explicit formats, redirected output, CI, dumb terminals, removed flags, and executable help.
Each case retains raw output, casts, key events, request logs, and its result.
The observer verifies binary hashes before and after execution.
Use repeated `--case` options for focused reruns.
`scripts/check-list-navigation.py` dispatches this observer and shares PTY helpers with other checks.

These captures show terminal replay.
They do not establish native Terminal appearance or clipboard behavior.
No installed CLI replacement, live API, or publication is required.

## 150-file trial

The large fixture contains six API pages with 25 synthetic files each.
Its IDs run from `file-demo-001` through `file-demo-150` across the complete dataset.
Filenames use the same sequence: `dataset-001.jsonl` through `dataset-150.jsonl`.
These numbers identify records across pages, so the last page clearly ends at 150.
Only the large fixture uses this numbering; default fixtures retain their existing IDs.
This command verifies all 150 files, lazy requests, cached back navigation, and terminal restoration:

```sh
python3 -I -B scripts/demos/list-pager/check-large.py \
  /absolute/path/to/openai-after /absolute/path/to/empty-checks \
  --asciinema /absolute/path/to/asciinema
```

Record all 150 entries with the same binary:

```sh
bash scripts/demos/list-pager/record-large.sh \
  /absolute/path/to/openai-after SOURCE_COMMIT /absolute/path/to/empty-output
```

The replay visits all six API pages, goes back from the final chunk, advances again, and quits.
The validator requires all 150 unique IDs across reconstructed recorded screens and exactly six API requests.
`after.gif` shows the complete interaction.
`after-first.png` and `after-final-page.png` show the first and final chunks.
The separate observer also verifies lazy requests and terminal restoration.

## First-Space boundary

The large observer accepts `--width` and `--height`; defaults are 98 columns and 20 rows.
At 101 columns and 27 rows, the first screen contains all 25 records from API page one.
The observer requires the first Space to fetch API page two when all loaded records are visible.

```sh
python3 -I -B scripts/demos/list-pager/check-large.py \
  /absolute/path/to/openai-after /absolute/path/to/empty-checks \
  --asciinema /absolute/path/to/asciinema --width 101 --height 27
```

Record the hint-only boundary regression with the same keys before and after:

```sh
bash scripts/demos/list-pager/record-page-boundary.sh \
  /absolute/path/to/openai-before BEFORE_COMMIT \
  /absolute/path/to/openai-after AFTER_COMMIT \
  /absolute/path/to/empty-output
```

Both recordings send Space, Space, `b`, Space, and `q` at 101 columns and 27 rows.
Before shows records 001–025 after the first Space; After shows records 026–050.
The representative recordings visit two API pages before and three after.
The separate large observer verifies all 150 records.
`before-first-space.png` and `after-first-space.png` show the direct comparison.
`comparison.gif` shows both interactions, including back navigation and the restored shell.
