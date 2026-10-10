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

## Discovery and recovery follow-up

`record-discovery.sh` compares the published draft with its discovery follow-up.
Its baseline is draft `e0e2c883800bae202151061778be6c598d0a1341`.
The retained baseline binary uses identical runtime source `664118a4021c4cba4b7b56ad6366eb12627c9107`.
The original main baseline `e6893982` belongs to the separate recipe demo above.
This recorder preserves the original demo sources and media.

Reserve a bounded PTY slot of at most five minutes before execution.
Provide a new output directory and independently verified source manifest.
Use the candidate's actual binary build commit as `CANDIDATE_FULL_COMMIT`.

```sh
export DEMO_BEFORE_SHA256=65878893f32bb115a01ea29a58d18019dafc1edd115ce10018eb360a5c817bed
export DEMO_AFTER_SHA256=CANDIDATE_BINARY_SHA256
export DEMO_SOURCE_MANIFEST=/absolute/path/to/follow-up-source-manifest.txt
PATH="$HOME/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/workflow-examples/record-discovery.sh --run-authorized-pty-slot \
  /absolute/path/to/prior-draft/openai \
  /absolute/path/to/candidate/openai \
  664118a4021c4cba4b7b56ad6366eb12627c9107 \
  CANDIDATE_FULL_COMMIT \
  /absolute/path/outside/repository/discovery-follow-up-demo
```

The fixed twelve scenes cover dark and light 80×24 viewports, plus a `NO_COLOR` 40×24 viewport.
Each profile compares bare `openai examples` and an unsupported YAML format.
The candidate scene copies its `Try:` command and prints the Files recipe.
Recording pauses one second after candidate failure guidance, before recovery scrolls the narrow viewport.
This pause belongs to the recorder; the CLI adds no delay.
The recorder copies that command from a separate diagnostic capture of the same immutable binary.
It verifies that the PTY prints identical guidance before validating the recovered recipe bytes.
It allows only `openai examples files --format text`; it never evaluates the printed API recipe.
Each failed command retains status 1, even when the subsequent recovery succeeds.

The baseline prints all 64 discovery lines directly into the PTY.
Its final screenshot shows the actual scrolled viewport, without filtering or cropping.
Complete casts and transcripts retain the earlier lines.
The candidate prints six discovery lines.
Each scene ends with a Before or After label that remains visible after scrolling.

All commands use empty homes, no credentials, and malformed remote configuration.
The recorder preserves exact stdout, stderr, command statuses, source hashes, and provenance.
It checks unchanged Files text and JSON output against the prior draft.
Each profile directory contains its comparison GIF and labeled screenshots.
The top-level `validation.json` records the checks.
Inspect all media before publication, including narrow recovery scrolling.
Release the PTY slot immediately after all recorder processes finish.
The screenshots remain terminal replays, without native graphical terminal or live API claims.
