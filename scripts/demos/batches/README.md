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
