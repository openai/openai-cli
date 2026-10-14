# List navigation recording

Generated files, batches, and projects commands do not yet call the navigation helpers on this branch.
Public activation requires the normal generator-source and SDK-promotion sequence.
The two-page recipe below requires a separately activated candidate and does not establish activation here.
Identify that candidate and its generated-source provenance when recording future integration evidence.

The shared runtime now includes both completion follow-ups:

- Complete first results that fit print once and exit without q, preserving the shell prompt row.
- Tab-aware display fitting retains navigation when the original output would overflow.

Models uses the shared fit policy through its existing single-response viewer.
Run its public boundary checks without generated cursor adapters:

```sh
python3 -I -B scripts/check-list-navigation.py BINARY OUTPUT_DIR \
  --case models-single-response \
  --case short-models-tab-expands-overflow \
  --case short-models-tab-cancels-wrap
```

The full checker and its cursor-resource short-result suite require generated activation.
Recheck files, batches, and projects after normal generated promotion before declaring SDK-1178 or SDK-1179 complete.
Historical activated recordings retain their original candidate identities.

Record identical commands against two synthetic API pages.
The recorder checks request counts before input and after Space.
It uses the shared asciinema and agg workflow.

Build the fixture:

```sh
go build -o /tmp/list-navigation-demo-api ./scripts/demos/list-navigation
```

Put asciinema, agg, ffmpeg, and ffprobe on `PATH`.
Provide pinned before and after binaries with their full commit hashes.
Run from the repository root:

```sh
DEMO_API_BINARY=/tmp/list-navigation-demo-api \
  bash scripts/demos/record-list-navigation.sh \
  BEFORE_BINARY AFTER_BINARY BEFORE_SHA AFTER_SHA OUTPUT_DIR
```

Use a new, empty output directory outside the repository.
The recorder writes GIFs, screenshots, transcripts, request logs, and timing evidence there.
Inspect both screenshots and the comparison GIF before sharing them.

Both scenes run `openai files list --limit 2 --max-items -1`.
The after scene waits 1.5 seconds, sends Space, then sends q after another 1.5 seconds.
The before scene must fetch both pages without input.
The after scene must fetch only one page before Space.

The viewer also supports `p` to print the current page and quit.
This action restores the terminal before printing complete labeled values.
It preserves long IDs without requesting another page.
The viewport wraps long IDs; use `p` for unbroken logical ID lines.
The PTY checker covers this action separately from the comparison recording.

The recording uses private PTYs and temporary homes.
The driver disables outer PTY output translation while it relays CLI bytes.
It restores the outer PTY before printing recorder annotations.
The validator compares the recorded CLI bytes with the driver's SHA256 digest.
The recorder uses agg's swash backend for consistent glyph placement across incremental redraws.
It demonstrates synthetic process behavior, not native graphical terminal appearance.
