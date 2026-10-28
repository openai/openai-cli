# Batch workflow demo

Build a baseline executable from the comparison commit.
Build the candidate executable from the reviewed candidate.
Build the synthetic API:

```sh
GOMAXPROCS=2 go build -p 2 -o /tmp/batches-demo-api ./scripts/demos/batches
```

Capture through the shared lifecycle:

```sh
DEMO_API_BINARY=/tmp/batches-demo-api bash scripts/demos/batches/record.sh \
  /tmp/openai-before /tmp/openai-after BEFORE_FULL_SHA AFTER_FULL_SHA /tmp/batches-recording
```

The recorder needs asciinema, agg, ffmpeg, ffprobe, and Python 3 on PATH.
Use a new output directory outside the repository.
Both scenes use real CLI processes and the same synthetic lifecycle and file bytes.
Before requires separate retrievals and a copied file ID.
After waits locally and saves available result files in one command.
The recorder verifies six GET requests, exact saved bytes, progress, receipts, and process exits.
It saves terminal replays, screenshots, transcripts, hashes, and request evidence.
Do not commit binary recordings.


## Try the workflow locally

Use the reviewed candidate executable and the synthetic API executable.
These steps submit no requests to a live service.
Start the synthetic API in one terminal:

```sh
mkdir -m 700 /tmp/f13-demo-session
/tmp/batches-demo-api /tmp/f13-demo-session/address /tmp/f13-demo-session/requests.jsonl
```

In another terminal, run the candidate with synthetic credentials and a loopback URL:

```sh
OPENAI_API_KEY=synthetic-demo-key OPENAI_BASE_URL="$(cat /tmp/f13-demo-session/address)" \
  openai batches wait batch_after --output-dir /tmp/f13-demo-session/results --poll-interval 1s
```

Use a new session directory for each run. Stop the synthetic API with Ctrl+C after the trial.
The local fixture finishes 1,000 requests and returns one output file with no error file.
The CLI preserves completed files if you repeat the command.

## Try the dashboard convenience workflows

Run the trial with an explicit candidate executable. No installation is required.

```sh
python3 scripts/demos/batches/try_workflows.py /absolute/path/to/openai
```

The trial uses synthetic responses and fake credentials. It makes no real API calls or charges.
It starts a loopback server and shows these actual CLI commands:

```sh
openai batches submit --file requests.jsonl --endpoint /v1/responses --completion-window 24h
openai batches wait batch_demo --output-dir results --poll-interval 1s
openai batches results batch_demo --custom-id sentiment-positive --match-input
```

The result view places the answer beside its original input.
The trial also checks list watching, input search, and complete JSON output.
It saves these additional command outputs without printing them by default.
Add `--show-checks` to display them.

Add `--simple` to try the shorter submission workflow:

```sh
openai batches submit requests.jsonl --wait --output-dir results
openai batches results batch_demo --custom-id sentiment-positive --match-input
```

Submit infers an omitted endpoint from the JSONL request URLs and defaults the completion window to 24h.
Prepare a valid JSONL request file first. Submit uploads that file; it does not write requests.
Submit prepares a private input copy and uploads those exact bytes.
Local submission needs writable temporary storage approximately equal to the input size.
Remove `--wait --output-dir results` to return immediately after creation.
Explicit endpoint, completion-window, metadata, expiration, JSON/YAML input, and connection options remain supported.
Use `--file requests.jsonl` instead of the positional path when needed.
The output directory must not contain existing output.jsonl or error.jsonl files.
If local waiting stops before completion, resume with the returned batch ID. Do not submit the file again.
If the batch has finished, inspect its status, results, and errors instead of waiting again.
Keep saved files. Use `batches download` only for files still needed.

The trial isolates CLI configuration and ignores personal API keys.
It checks three labels, reordered result IDs, exact downloaded bytes, and one upload plus one Batch creation.
It stops the CLI processes and server before returning.
It retains command logs, request evidence, results, and cleanup status in the printed temporary directory.
Use `--output-dir /absolute/new/directory` to choose that directory. Keep artifacts outside the repository.

This trial does not establish live API or native terminal compatibility.

## Try the interactive batch browser

Search filters the loaded page. Ctrl+R reloads the first page and preserves the search text.
Arrow keys select a batch. Enter opens it. Escape returns or exits.
Up from the first batch enters Search. Down returns to the matching batches.
Typing or pasting also starts Search. Search shows a caret; the batch list shows the selected row.
Browser menus use the full terminal, with a rounded panel and keyboard controls at the bottom.
Small terminals omit the panel to preserve space for navigation.
The batch overview shows status and request counts. Ctrl+R refreshes that batch without opening another screen.
View results opens first. Batch details shows timestamps, metadata, and validation errors.
Read actions remain inside the browser. Press `p` in a document to print it into shell scrollback.

Run this trial in a terminal without pipes or redirection:

```sh
python3 scripts/demos/batches/try_browser.py /absolute/path/to/openai
```

The browser shows three synthetic batches: successful, partially failed, and still processing.
Choose a batch, then open View results, View errors, View input, Batch details, or Save a file.
View errors hides only when the snapshot proves success and explicitly contains no error file or validation errors.
Missing or unfamiliar error metadata keeps View errors available. Validation diagnostics remain available alongside error-file content.
The overview summarizes consistent counts. Unknown or inconsistent counts retain explicit labels.
The three examples keep fixed statuses. Guided creation uses separate synthetic batches.
The fixture accepts only fake credentials.
Relative download paths use a temporary directory, printed when the demo closes.
Escape returns to batch actions or the batch list.
View input, View results, and View errors open a searchable request list when their file exists.
Request search covers the complete selected file, including text outside each preview.
Each row shows its record number, request ID, and preview. Duplicate IDs retain separate rows.
Press Enter to open one request. Escape returns to the same search and selection.
Request details show content before settings. Press `j` to switch between Readable and Raw JSON.
Result details place the request ID beside its reported outcome, followed by a separate Output block.
Simple token counts share one secondary line. Extended usage and unfamiliar records retain their complete readable details.
Blue identifies headings and selectable IDs. Output text keeps the terminal foreground.
Outcome colors supplement explicit words. Refusals and incomplete results do not receive a green completion indicator.
Input details use blue section headings and muted setting labels. Input content keeps the normal foreground.
Press Ctrl+V from the request list to view all records.
Batch details and missing-file notices open a framed document viewer with wrapped text and fixed controls.
Batch details places status and counts first, followed by timing, files, usage, and metadata.
Validation errors appear before timing. Labels align within each section.
Complete Batch IDs remain in the document for wrapping and printing.
Unfamiliar fields keep the complete readable fallback. Standalone command output stays unchanged.
Use arrows to scroll, Page Up/Down to change pages, and Home/End to jump.
Press `p` to print the complete readable document into terminal scrollback.
Press Enter or Escape after printing to return to the document.
The viewer keeps missing-file notices and errors inside the same frame.
Save a file is the last action. Press Enter to expand its Output, Error, and Input file choices.
Press Enter on a file choice to edit its suggested filename. The browser shows the destination folder.
Relative filenames use the current working directory. Paths beginning with `~/` use your home directory.
Existing files are never overwritten.
Escape collapses the choices before returning to the batch list. Right expands; Left collapses.
Canceling a destination returns to the same file choice. Each file choice keeps its own destination draft.
At the batch list, Escape exits. Ctrl+C exits from any screen.
The trial stops its child process and loopback server before returning.
It does not use personal API keys, install the CLI, or make live API calls.

Press Ctrl+N at the batch list to create a batch.
Choose a local JSONL file or an uploaded input file.
Review the endpoint, project, input file, and optional settings.
Only Ctrl+S on the review screen submits the batch. Enter does not submit.
The browser opens the created batch and updates its progress every ten seconds.
Updates stop when processing finishes or you leave its action screen.
Choose View results or Save a file after processing finishes.
The demo includes `requests.jsonl` and completes created batches after three progress updates.
Real processing can take up to the selected completion window. Leaving the browser does not cancel remote work.

Capture the complete synthetic workflow through the actual terminal interface:

```sh
python3 scripts/demos/batches/browser_guided_capture.py \
  /absolute/path/to/openai /absolute/new/capture-directory
```

The capture checks one upload, one creation, automatic completion, result inspection, saving, and terminal cleanup.
It retains terminal recordings, screen text, and request evidence outside the repository.

## Record the convenience comparison

Use the baseline and candidate binaries from the reviewed source commits.

```sh
bash scripts/demos/batches/record-workflows.sh \
  /absolute/path/to/openai-before /absolute/path/to/openai-after \
  BEFORE_FULL_SHA AFTER_FULL_SHA /absolute/new/recording-directory
```

The recorder requires Python 3, asciinema, agg, ffmpeg, and ffprobe.
It reuses the shared capture lifecycle and a separate Python fixture.
The original Go fixture remains unchanged.
Before shows separate upload and creation commands.
After shows submission, readable lifecycle details, input discovery, and matched results.
The recorder checks one upload and one Batch creation for each comparison.
It retains replay files, screenshots, transcripts, hashes, and request evidence outside the repository.
Do not commit binary media.

## Record the browser comparison

Use two built CLI executables and their complete commit IDs:

```sh
bash scripts/demos/batches/record-browser.sh \
  /absolute/path/to/before /absolute/path/to/after \
  BEFORE_COMMIT AFTER_COMMIT /tmp/batch-browser-recording
```

The recipe requires existing `asciinema`, `agg`, `ffmpeg`, and Python installations.
It reuses the shared capture lifecycle and the synthetic browser fixture.
It checks aligned menu lines, readable results, errors, successful exits, and GET-only requests.
The recipe records color by default. Set `NO_COLOR=1` for a monochrome replay.
Captures show terminal replay. They do not establish physical terminal appearance or live API behavior.

The pinned agg 1.9.0 renderer ignores recorded resize events. Use fixed-size captures for visual comparisons.
Keep native resize probes separate. Mixed-size agg replays can retain incorrect scroll margins and show false artifacts.
