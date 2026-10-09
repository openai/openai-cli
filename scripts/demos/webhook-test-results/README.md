# Webhook test result terminal replays

This recipe compares the existing webhook test command against synthetic API results.
The API returns HTTP 200 with `success: true` and receiver status 500.
The baseline shows `Success: true` beside `Status code: 500`.
The candidate explains that the test completed and the receiver returned HTTP 500.
A separate candidate scene shows receiver status 200.
Every command must exit zero because the test API request completed.
The fixture never contacts a webhook receiver.

Use existing Python, asciinema, agg, ffmpeg, and ffprobe installations.
Build the baseline from `e68939820415144d769ed02de6aa72d5b7d32948`.
Build the candidate independently.
Verify binary provenance before recording.
The recipe records supplied commits and hashes but cannot establish their relationship.
Set `DEMO_AFTER_SOURCE_STATE` when the candidate contains uncommitted changes.

Run each width with a fresh output directory outside the repository:

```sh
DEMO_WIDTH=80 PATH=/path/to/replay-tools:$PATH \
  bash scripts/demos/webhook-test-results/record.sh \
  /path/to/before/openai /path/to/after/openai \
  e68939820415144d769ed02de6aa72d5b7d32948 AFTER_SHA \
  /path/outside/repository/webhooks-80

DEMO_WIDTH=40 PATH=/path/to/replay-tools:$PATH \
  bash scripts/demos/webhook-test-results/record.sh \
  /path/to/before/openai /path/to/after/openai \
  e68939820415144d769ed02de6aa72d5b7d32948 AFTER_SHA \
  /path/outside/repository/webhooks-40
```

`AFTER_SHA` must be the full candidate commit ID.
The shared `../capture_and_render.sh` lifecycle owns recording, rendering, fixture cleanup, and temporary files.
The Python fixture binds an ephemeral loopback port.
It checks request paths, event type, and synthetic credentials without logging credentials.
Its signal handler stops the server and waits for request threads.
Fixture failures and shutdown failures fail the recording.

Each command runs with a temporary home, fake credentials, and an explicit environment.
No personal CLI configuration, proxy settings, or shell hooks enter the command environment.
Redirected probes use `--format text` and capture stdout and stderr separately.
Terminal scenes use the default readable output and merge stdout with stderr.
The recipe checks exact candidate output, baseline labels, empty probe stderr, and process exit statuses.
It also checks identical before/after response hashes and unchanged binaries during recording.
Long sentences wrap at 40 columns.
Transcript checks require complete text after wrapping but do not prove visual quality.

`comparison.gif`, `before.png`, and `after.png` show the receiver500 comparison.
`accepted.gif` and `accepted.png` show receiver200.
Each scene includes its cast, transcript, redirected output, and status evidence.
`metadata.txt` records commit inputs, environment, tools, binary hashes, and capture statuses.
`requests.jsonl` records only synthetic scenario names, response hashes, and statuses.

Inspect all PNGs and GIF frames for clipping, spacing, timing, and sensitive data before sharing.
These artifacts show terminal replay with synthetic data.
They do not establish native terminal appearance or live API behavior.
Keep media and binaries outside Git.
