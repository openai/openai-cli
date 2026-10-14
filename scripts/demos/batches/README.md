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
