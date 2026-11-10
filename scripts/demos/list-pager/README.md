# Automatic list pager evidence

The inline preview shows as many loaded rows as fit in the terminal.
Space opens the full-screen view at the first unread row. At the end of the loaded records, Space fetches one API page.
The footer identifies the visible file range. The table keeps its headings on screen.
Resource pagers omit the optional JSON-format hint.
This prevents a hint-only Space step when all loaded rows already fit.
`b` returns to loaded chunks without another request.
`q` restores the original terminal screen.
Short or empty complete results print once and return to the shell.
After entering the full-screen view, the last screen stays open until the user quits.
The pager ignores `i` and `p`.
An API failure preserves received records after restoring the terminal.
Explicit formats, redirected output, CI, and dumb terminals retain their existing paths.

The fixture serves synthetic Files, Batches, Projects, and Models data on loopback.
The recording compares the historical inline default with the terminal-sized preview.
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
It also checks Models, explicit formats, redirected output, CI, dumb terminals, list flags, and executable help.
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
At 101 columns and 27 rows, the preview shows the first 25 records.
Space requests the second API page and shows records 26–50.

```sh
python3 -I -B scripts/demos/list-pager/check-large.py \
  /absolute/path/to/openai-after /absolute/path/to/empty-checks \
  --asciinema /absolute/path/to/asciinema --width 101 --height 27
```

Record the change in demand-driven API paging with the same keys before and after:

```sh
bash scripts/demos/list-pager/record-page-boundary.sh \
  /absolute/path/to/openai-before BEFORE_COMMIT \
  /absolute/path/to/openai-after AFTER_COMMIT \
  /absolute/path/to/empty-output
```

Both recordings send Space, Space, `b`, Space, and `q` at 101 columns and 27 rows.
The v1.51.5 preview shows records 006–030 after the first Space. The candidate shows records 026–050.
Both recordings visit three API pages. Back reuses cached records.
The separate large observer verifies all 150 records.
`before-first-space.png` and `after-first-space.png` show the direct comparison.
`comparison.gif` shows both interactions, including back navigation and the restored shell.

## Webhooks and Skills

Build the base and candidate CLI binaries. Run this recorder on macOS with Python 3, Pillow, and asciinema 3.

```sh
python3 -B scripts/demos/list-pager/record-resources.py /path/to/base/openai /path/to/candidate/openai /tmp/list-pager-demo
```

The recorder uses 76×12 PTYs and a loopback API with identical synthetic records.
The previous default printed all 30 records. The new default shows ten rows, then waits.
Space requests the second API page. Back returns to the first ten rows from the cache.
The GIF also shows `--browse=false` printing all 30 rows without waiting.
The title bar labels each capture. The terminal contents and footer come from the CLI.
