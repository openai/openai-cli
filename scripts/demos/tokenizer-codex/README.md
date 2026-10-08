# Local tokenizer and Codex demos

This recorder compares actual CLI binaries through the shared `capture_and_render.sh` lifecycle.
It starts no API fixture, opens no browser, and executes no installation instructions.
Its scenes remove the shared helper's synthetic API key before invoking either binary.
Each process receives a rejecting loopback base URL and no personal environment or shell initialization.

## Scenes

| Mode | Command | Before status | After status |
| --- | --- | --- | --- |
| `count` | `openai tokenizer count --text "Hello, world!"` | 1 | 0 |
| `inspect` | `openai tokenizer inspect --text "Hi!"` | 1 | 0 |
| `codex` | `openai codex --destination config` | 1 | 0 |
| `guide` | `openai codex` | 3 | 0 |
| `editor` | `openai tokenizer` | 0 | 130 |

The baseline is main `da762ffff4f35732f4720ac4db531d8d764f2cbe`.
Direct baseline execution verified these statuses before recording.
The first three baseline commands report an unrecognized option.
The guide baseline reports an unknown help topic.
Recordings preserve those actual errors; they do not substitute explanatory output.
The editor scene uses published feature commit `6e761e71051aa260e01d189437a77dd5e9f72365` as its baseline.
That baseline prints tokenizer help and exits successfully.
The candidate opens the editor; the driver exits through Ctrl+C and preserves status 130.

The default `o200k_base` count example contains 13 input bytes and four tokens.
The inspect example contains three input bytes and two tokens.
Its token bytes are `4869` and `21`.
Public regression tests verify exact token IDs and boundaries separately.

## Record

Coordinate the shared native PTY allocation before running this script.
Build and verify both binaries independently.
Supply full 40-character comparison commits and an empty output directory outside the repository.

```sh
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh count \
  /absolute/path/to/openai-before /absolute/path/to/openai-after \
  da762ffff4f35732f4720ac4db531d8d764f2cbe "$AFTER_SHA" \
  /absolute/path/outside/repository/count-80
```

Repeat with `inspect`, `codex`, and optionally `guide` in separate output directories.
Set `DEMO_COLUMNS=40` for a narrow recording.
The default width is 80 columns.
The recorder reserves additional rows for the guide and narrow recordings.

```sh
DEMO_COLUMNS=40 \
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh inspect \
  /absolute/path/to/openai-before /absolute/path/to/openai-after \
  da762ffff4f35732f4720ac4db531d8d764f2cbe "$AFTER_SHA" \
  /absolute/path/outside/repository/inspect-40
```

Set `DEMO_SOURCE_MANIFEST` to a source-hash manifest for an uncommitted development candidate.
The recorder retains that manifest and available Go build information.
It logs binary hashes, supplied commits, platform details, capture tools, and actual process statuses.
These records do not independently prove that a binary matches a supplied commit.

The shared helper requires an executable fixture argument.
This recorder supplies the after binary in that unused position.
It never calls `demo_start_api` or executes that argument as a server.
The rejecting endpoint prevents accidental use of a live API endpoint.
Separate process-trap tests establish the local commands' zero-request contract.

## Validate and inspect

The recorder automatically validates transcripts and exit statuses before assembling the comparison GIF.
Run the same validation independently:

```sh
python3 scripts/demos/tokenizer-codex/validate.py /absolute/path/to/capture inspect
```

Inspect `before.png`, `after.png`, and actual `comparison.gif` frames before sharing.
Check wrapping, clipping, timing, complete output, and correct labels at each recorded width.
Keep media outside Git.
Retain only these reproducible scripts and instructions in the repository.

These recordings show real macOS PTY execution with replay rendering.
They do not prove graphical terminal appearance or native Windows/Linux execution.
No recording has occurred merely because this recipe exists.

## Interactive editor and guide refinement

Use the published feature baseline for the editor and the updated Codex guide.
Set `DEMO_BEFORE_STATUS=0` for the guide when comparing against that baseline.
The original main baseline still uses the guide's default status 3.
The recorder saves expected statuses separately from actual process statuses.

```sh
DEMO_THEME=dark DEMO_BEFORE_STATUS=0 \
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh editor \
  /absolute/path/to/published-feature /absolute/path/to/candidate \
  6e761e71051aa260e01d189437a77dd5e9f72365 "$AFTER_SHA" \
  /absolute/path/outside/repository/editor-dark-80
```

Repeat with `DEMO_COLUMNS=40` for the compact layout.
Use `DEMO_THEME=light` for a light terminal.
Use `DEMO_THEME=no-color` to set `NO_COLOR=1`.
The editor defaults to dark; existing scenes retain their no-color default.
Editor dimensions are 80×32 or 40×44, preserving room for the baseline's help output.

The Bash scene uses a small Python keyboard driver inside the shared capture lifecycle.
The driver starts the actual CLI in a terminal with matching dimensions.
It types `Hello, ` and pastes the remaining synthetic UTF-8 text.
It visits Text, Token IDs, Bytes, token details, the alternate encoding, and controls.
It forwards only the CLI's actual terminal output.
It answers terminal capability queries and stops its process group on interrupted recording.
No fabricated application frames enter the recording.

The scene retains its exact synthetic input, observed states, dimensions, theme, and actual exit status.
The validator selects screenshot times from actual output events in `after.cast`.
It requires distinct visible states before generating screenshots.
`after.png` shows the live Text view before exit.
`after-ids.png`, `after-bytes.png`, `after-details.png`, `after-encoding.png`, and `after-controls.png` preserve the other states.
`after-exit.png` retains the final shell frame.
The comparison GIF includes the complete interaction and cleanup.

Use `guide` with the same baseline, theme, and before-status setting for the Codex presentation comparison.
The guide scene prints instructions only.
Review every selected screenshot and the GIF before publication.
