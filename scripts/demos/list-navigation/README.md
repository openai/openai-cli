# List navigation recording

This prerequisite contains handwritten navigation helpers only.
Generated commands do not call them on this branch.
Public activation requires the normal generator-source and SDK-promotion sequence.
The recipe below requires a separately activated candidate and must not prove activation of this prerequisite.
Identify that candidate and its generated-source provenance when recording future integration evidence.

Before generated activation, carry both reviewed handwritten follow-ups:

- `51bac6bca356f067ee671add8bac630b8aa87658`: one-screen completion prints the complete fitting result once and exits without q.
- `f4c1b76603b5830e61143255a3cddd25a9ee09af`: tab-aware display fitting preserves the shell prompt row.

This inert draft does not complete SDK-1178 or SDK-1179.
Recheck the affected public commands after those fixes and normal generated promotion.
Historical activated recordings remain integration evidence, not public behavior of this prerequisite.

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
