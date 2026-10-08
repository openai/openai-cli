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
| `editor` | `openai tokenizer` | 0 for help; 130 for legacy or Options editors | 130 |

The baseline is main `da762ffff4f35732f4720ac4db531d8d764f2cbe`.
Direct baseline execution verified these statuses before recording.
The first three baseline commands report an unrecognized option.
The guide baseline reports an unknown help topic.
Recordings preserve those actual errors; they do not substitute explanatory output.
The editor scene defaults to the historical help baseline at `6e761e71051aa260e01d189437a77dd5e9f72365`.
That baseline prints tokenizer help and exits successfully.
Use `DEMO_EDITOR_BEFORE=legacy` for the previous interactive editor at `30a5e9186f7b0d8129401622cd4507bf90b37d55`.
This comparison drives both actual editors and preserves their Ctrl+C status 130.
Use `DEMO_EDITOR_BEFORE=options` for visual refinements against `9fb91c176c9f70d09cf806648c2f17b1654a292a`.
Both editors then use the same Options controls.
The retained comparison binary embeds `e990b0bf4355ad0a934d0c4ffc0d5fe53628ad0d`; its runtime matches published `9fb91c1`.
Record the actual binary's build commit in capture metadata.
The candidate uses the current Options layout in every comparison.

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

Use the previous Options editor when reviewing focus, alignment, and spacing refinements.
The driver uses matching controls on both sides of that comparison.
The recorder saves both layouts, expected statuses, and actual process statuses.

```sh
DEMO_THEME=dark DEMO_EDITOR_BEFORE=options \
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh editor \
  /absolute/path/to/previous-options-editor /absolute/path/to/candidate \
  e990b0bf4355ad0a934d0c4ffc0d5fe53628ad0d "$AFTER_SHA" \
  /absolute/path/outside/repository/editor-dark-80
```

The earlier controls comparison remains available with `DEMO_EDITOR_BEFORE=legacy` and the `30a5e91` full commit above.
That mapping follows the older editor's focus and encoding controls.
Omit `DEMO_EDITOR_BEFORE` to retain the historical help-to-editor recipe.
For that comparison, provide the help-only binary and its `6e761e7` full commit above.
Do not label a help-only baseline as the previous interactive experience.
Both interactive baseline variants require status 130; the help baseline requires status zero.

Repeat with `DEMO_COLUMNS=40` for the compact layout.
Use `DEMO_THEME=light` for a light terminal.
Use `DEMO_THEME=no-color` to set `NO_COLOR=1`.
The editor defaults to dark; existing scenes retain their no-color default.
Editor dimensions are 80×32 or 40×44, also accommodating the historical baseline's help output.

The Bash scene uses a small Python keyboard driver inside the shared capture lifecycle.
The driver starts the actual CLI in a terminal with matching dimensions.
It types `Hello, ` and pastes `tokens! 👋` and `Café.` around an Enter newline.
Both layouts therefore receive the exact same 26-byte text.
The candidate's default Text and Token IDs views show token count without the advanced byte count.
The driver tabs to Options and switches to Token IDs with Right.
Enter opens the View chooser; Down and Enter apply Bytes.
Tab moves to Results, where arrows select the partial Unicode token.
The driver holds that focused selection before Enter opens its details.
Escape closes details; another Escape returns to Text.
The driver then opens the Tokenizer chooser and applies `cl100k_base`.
It also opens the controls reference before exiting through Ctrl+C.
The previous editor follows its existing focus and encoding controls.
It forwards only the CLI's actual terminal output.
It answers terminal capability queries and stops its process group on interrupted recording.
No fabricated application frames enter the recording.

The scene retains its synthetic input actions, observed states, layout, dimensions, theme, and actual exit status.
The validator selects screenshot times from each driven editor's actual output events.
It requires distinct visible states before generating screenshots.
It checks the partial token's ID, byte boundaries, and complete hexadecimal bytes.
It also checks the candidate's View and Tokenizer labels and count-only default status.
`after.png` shows the live Text view before exit.
`after-ids.png`, `after-bytes.png`, `after-details.png`, `after-encoding.png`, and `after-controls.png` preserve the other states.
`after-results.png` shows the selected token with Results focus before Details.
`after-view-choice.png` and `after-tokenizer-choice.png` show the candidate's selection menus.
`after-exit.png` retains the final shell frame.
Interactive comparisons create corresponding `before-*` snapshots, including `before-results.png`.
An Options baseline also retains both chooser snapshots.
Historical captures retain validation support for their original state sequences.
Its `before.png` also shows the live Text view.
The help-only comparison retains the historical help screenshot as `before.png`.
The comparison GIF includes the complete interaction and cleanup.

## Cursor-linked selection and legacy tokenizers

Set `DEMO_EDITOR_LINKED=1` when the after binary supports cursor-linked selection and all four tokenizers.
This optional mode applies only to the after editor.
The before binary keeps its selected help, legacy, or Options workflow.
Earlier recipes keep their original behavior when this setting is absent.

```sh
DEMO_EDITOR_LINKED=1 DEMO_EDITOR_BEFORE=options DEMO_THEME=dark \
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh editor \
  /absolute/path/to/previous-options-editor /absolute/path/to/candidate \
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/editor-linked-dark-80
```

Use the actual build commits for both binaries.
For example, a verified `b542a47` Options binary provides the earlier behavior without cursor-linked selection.
The existing width and theme settings also apply to this mode.

After typing the unchanged 26-byte fixture, the driver presses Home and Right within `Café.`.
The caret reaches byte 21 and selects token 9 of 10, containing `afé`.
The driver requires both `C▏afé.` in the editor and the dependent `·["afé"]` token marker.
The new `after-caret.png` captures that state before the existing Token IDs stage.
The Results stage first presses Home, then moves four tokens to the split emoji fragment.
This preserves its exact-byte comparison when typing initially selects the final token.

After the `cl100k_base` stage, the driver selects `r50k_base` and `p50k_base` in order.
Each legacy tokenizer produces 11 tokens and 26 input bytes for this fixture.
The recorder retains these settled states as `after-r50k.png` and `after-p50k.png`.
The controls stage and Ctrl+C cleanup follow normally.

Linked reports use capture version 3 and record the exact caret and tokenizer actions.
`editor-linked.tsv` records the requested mode and keeps the before scene unlinked.
Version 3 matches stage conditions within the latest inline frame.
This prevents an earlier equal token count from validating a later tokenizer's Updating frame.
The driver and validator still use bounded buffers and actual CLI output.
Versions 1 and 2 retain their historical validation paths.
All three extra PNGs must exist before the recorder can report success.

## Model-family presentation

Add `DEMO_EDITOR_PRESENTATION=models` to the linked editor recipe for the model-family labels.
This setting applies only to the after scene and requires `DEMO_EDITOR_LINKED=1`.
The before scene retains its encoding labels, including the `8990809` baseline.

```sh
DEMO_EDITOR_PRESENTATION=models DEMO_EDITOR_LINKED=1 DEMO_EDITOR_BEFORE=options DEMO_THEME=dark \
PATH="/Users/vguvvala/.cache/cli-terminal-replay/bin:$PATH" \
  bash scripts/demos/tokenizer-codex/record.sh editor \
  /absolute/path/to/previous-editor /absolute/path/to/candidate \
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/editor-models-dark-80
```

Version 4 checks the `Model` row and `Choose model` chooser.
The four labels are `GPT-5.x & o1/o3`, `GPT-4 & GPT-3.5`, `GPT-3`, and `Codex / Davinci`.
The first label carries `Default`; the others carry `Legacy`.
Validation requires each complete chooser row, so `GPT-3` cannot match part of `GPT-3.5`.
Primary frames must omit raw encoding names, the F1 hint, and the Enter-newline reminder.
The driver still uses F1 to inspect controls.
Exact-byte details retain their encoding metadata.
The fixture, stage names, counts, cursor checks, and cleanup remain unchanged.
`editor-presentation.tsv` records the requested presentation independently of the older capture settings.
Earlier recipes and capture versions 1 through 3 retain their validation behavior.

For the Codex presentation comparison, use `guide` with the historical published-feature baseline and `DEMO_BEFORE_STATUS=0`.
The original main baseline still uses the guide's default status 3.
The guide scene prints instructions only.
Review every selected screenshot and the GIF before publication.
