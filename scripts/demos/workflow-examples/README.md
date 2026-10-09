# Workflow examples terminal replay

This recorder compares the missing command with the new offline recipes.
It prints recipes without executing their API commands.
It uses the shared `scripts/demos/capture_and_render.sh` lifecycle.
It creates no capture framework or API fixture.

Reserve the shared PTY slot before running this recorder.
Build the candidate separately under an authorized source-build reservation.
Use verified immutable binaries and full source commits.
Retain each binary's source provenance in `DEMO_SOURCE_MANIFEST`.
The manifest must identify both binary hashes and their source commits.
Record dirty-source manifests explicitly during development.
Use a clean committed candidate for publication evidence.

The pinned baseline is `e68939820415144d769ed02de6aa72d5b7d32948`.
Its retained binary is:

```text
/absolute/path/to/baseline/openai
SHA256: 23414722af83f1d4acfd1e80b1723448fc711083d7b76b9bcf36ee7813ff3df2
```

The recorder requires Bash, Python 3, asciinema, agg, ffmpeg, ffprobe, and local Menlo.
Use existing reviewed tools. Do not install capture dependencies during recording.
Set the following values to the independently verified candidate and manifest:

```sh
export DEMO_BEFORE_SHA256=23414722af83f1d4acfd1e80b1723448fc711083d7b76b9bcf36ee7813ff3df2
export DEMO_AFTER_SHA256=CANDIDATE_BINARY_SHA256
export DEMO_SOURCE_MANIFEST=/absolute/path/to/source-manifest.txt
PATH="$HOME/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/workflow-examples/record.sh --run-authorized-pty-slot \
  /absolute/path/to/baseline/openai \
  /absolute/path/to/candidate/openai \
  e68939820415144d769ed02de6aa72d5b7d32948 \
  CANDIDATE_FULL_COMMIT \
  /absolute/path/outside/repository/workflow-examples-demo
```

Choose a new empty output directory outside Git.
The authorization flag records the operator's reservation; it does not acquire a slot.

The comparison records `openai examples files` before and after the change.
Two additional scenes print audio and model recipes.
All four comparison scenes use 80 columns and 28 rows.
A separate Files scene uses 40 columns and 34 rows.
All scenes use `NO_COLOR`, isolated Bash environments, and empty working directories.
The recorder removes API credentials from each scene.
The baseline uses a loopback-only base URL.
The candidate uses an invalid base URL and missing mTLS paths.
No request-counting fixture runs, and no live endpoint is configured.

The recorder checks status 3 before and status 0 after.
It checks candidate stdout against separate pipe output for every topic.
It compares Files recipe bytes at 40 and 80 columns.
The comparison normalizes only PTY CRLF conversion.
It reads raw asciicast output events before visual wrapping occurs.
It also checks empty pipe stderr and unchanged empty working directories.
Copied workflow execution belongs to the feature's separate synthetic regression tests.

The output includes:

- `comparison.gif`: labeled Before, After Files, After audio, and After models.
- `before.png` and `after.png`: labeled Files comparison screenshots.
- `after-audio.png`, `after-models.png`, and `narrow.png`: additional screenshots.
- Individual GIFs, raw `.cast` files, and text transcripts.
- Exact pipe output, empty stderr captures, and `validation.json`.
- Binary hashes, source provenance, tool versions, and retained capture scripts.

Inspect the GIF and every screenshot before publication.
Check readable command rows, complete text, timing, and sensitive information.
Label these assets as terminal replays.
They do not prove graphical terminal, font, Windows, or Linux behavior.
Keep media outside Git and attach reviewed assets to the PR.
