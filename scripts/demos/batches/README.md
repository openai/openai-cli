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

The trial isolates CLI configuration and ignores personal API keys.
It checks three labels, reordered result IDs, exact downloaded bytes, and one upload plus one Batch creation.
It stops the CLI processes and server before returning.
It retains command logs, request evidence, results, and cleanup status in the printed temporary directory.
Use `--output-dir /absolute/new/directory` to choose that directory. Keep artifacts outside the repository.

This trial does not establish live API or native terminal compatibility.

## Try the interactive batch browser

Run this trial in a terminal without pipes or redirection:

```sh
python3 scripts/demos/batches/try_browser.py /absolute/path/to/openai
```

The browser shows three synthetic batches: successful, partially failed, and still processing.
Choose a batch, then inspect its status, results, errors, inputs, or downloads.
The fixture keeps these statuses fixed. It accepts only read operations and fake credentials.
Relative download paths use the printed temporary directory.
Escape returns to batch actions or the batch list.
At the batch list, Escape exits. Ctrl+C exits from any screen.
The trial stops its child process and loopback server before returning.
It does not use personal API keys, install the CLI, or make live API calls.

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
Captures show terminal replay. They do not establish physical terminal appearance or live API behavior.
