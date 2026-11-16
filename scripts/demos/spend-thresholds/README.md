# Spend threshold terminal replays

This recipe compares real CLI binaries against fixed synthetic responses.
It uses the shared `capture_and_render.sh` lifecycle and a Python loopback fixture.
It requires no fixture build, installation, live API request, or billing change.

The recordings are Bash PTY terminal replays, not native graphical terminal validation.
Each command uses fake admin credentials and an isolated configuration directory.
The fixture checks request authentication without logging credentials or request headers.

## Record

Reserve a PTY execution slot before running this recipe.
Prepare independently verified binaries for the baseline and candidate commits.
Provide their full 40-character commit IDs.
The recorder records binary hashes but cannot establish build provenance.

Required tools are Python 3, Bash, asciinema, agg, ffmpeg, ffprobe, and shasum.
Use an empty output directory outside the repository.

```sh
bash scripts/demos/record-spend-thresholds.sh --run-authorized-pty-slot \
  /path/to/before/openai /path/to/after/openai \
  "$BEFORE_SHA" "$AFTER_SHA" /path/to/evidence/spend-thresholds
```

Optionally set `DEMO_SOURCE_MANIFEST` to a verified source/build manifest.
The recorder copies and hashes that manifest alongside the evidence.
The authorization flag records the operator's decision; it does not reserve a slot.

The initial comparison base is `e68939820415144d769ed02de6aa72d5b7d32948`.
The batch coordinator verified a shared baseline binary with this SHA-256:

```text
23414722af83f1d4acfd1e80b1723448fc711083d7b76b9bcf36ee7813ff3df2
```

Verify any reused binary against its own build record and digest.

## Scenes and assertions

Both 100-column and 40-column captures include these before/after scenes:

- Organization limit: raw `10000` becomes `USD 100.00 per month`; reported enforcement stays visible.
- Project alert: raw `20000` becomes `USD 200.00 per month`; readable output identifies notifications without caps.
- Explicit JSON: original cents and response fields remain unchanged.
- Missing enforcement: readable output states `not reported in this response`.

The fixture serves one alert with `has_more: false`.
This replay does not establish multi-page behavior; dedicated public tests cover pagination.
Commands use existing canonical routes and mutation-free GET requests.

The recorder checks all command exit statuses and fixture shutdown.
The validator checks every transcript, PTY width, request path, and response hash.
Fixture request, response-write, flush, and close failures stop the recording.
SIGTERM closes the fixture cleanly with status zero.

Each scene produces an asciicast, GIF, PNG, and plain transcript.
`comparison-100.gif` and `comparison-40.gif` contain the respective complete comparisons.
Metadata records commits, binary/tool hashes, environment, and command exit statuses.
`fixtures.json`, `requests.jsonl`, `validation.txt`, and `SHA256SUMS` retain reproducible evidence.
Review screenshots before publication. Keep generated media outside Git.
