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
After waits locally and resolves the output file directly from the batch.
The recorder verifies seven GET requests, exact saved bytes, progress, receipts, and process exits.
It saves terminal replays, screenshots, transcripts, hashes, and request evidence.
Do not commit binary recordings.
