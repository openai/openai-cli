# Compact list table evidence

This recipe compares Files, Batches, and Projects table presentation.
Use `scripts/demos/list-pager/` for the current pager interaction comparison.
Models shares the pager runtime but uses a separate resource presentation.

The fixture serves synthetic data on loopback.
The recorder uses the shared capture lifecycle and isolated configuration.
Both binaries receive identical responses and terminal settings.
The current candidate opens automatic terminal results in the pager.
The recorder waits for loaded results and sends `q`.
The pager restores the original screen without printing results into terminal history.
An API failure preserves received records after restoring the terminal.

Build the synthetic API fixture:

```sh
GOMAXPROCS=2 go build -p 2 -o /tmp/list-table-demo-api ./scripts/demos/list-tables
```

Record a comparison with exact source commit identities:

```sh
DEMO_API_BINARY=/tmp/list-table-demo-api \
  bash scripts/demos/list-tables/record.sh \
  /tmp/openai-before /tmp/openai-after BEFORE_SHA AFTER_SHA /tmp/list-tables-wide 110
```

Use width `40` for a narrow comparison.
Append `--resource files` to limit the comparison to one resource.
Scenes use plain output by default: `DEMO_COLOR=0` sets `NO_COLOR=1` and `FORCE_COLOR=0`.
Add `DEMO_COLOR=1` before `bash` to enable the normal terminal colors.
Color scenes set `COLORTERM=truecolor`, `COLORFGBG=15;0`, and `TERM_PROGRAM=Apple_Terminal`.
Color scenes leave `NO_COLOR` and `FORCE_COLOR` unset in the isolated environment.
Both modes use `TERM=xterm-256color` and the Dracula replay theme.
The metadata records these scene settings.
This option does not change the validator or page checker environments.
Each output directory must be empty and outside the repository.
The recipe requires Go, Python 3, asciinema, agg, ffmpeg, and ffprobe.
Do not replace the installed CLI.
Record uncommitted changes beside their binary hashes.

Each scene requires one API request and exit status zero.
The scene driver preserves CLI bytes and records their SHA256 digest.
Files named `*-visible.png` and `*-visible.txt` show the loaded page before quitting.
The complete cast and final transcript retain the restoration sequence.
The recorder renders actual terminal bytes with agg's swash renderer.
The fixtures include Unicode filenames and complete IDs.

The validator compares pipe output and explicit formats between both binaries.
It preserves exact stdout, stderr, exit statuses, and API request counts.
It also checks extraction, raw output, item limits, empty results, unfamiliar fields, and partial failures.
It checks explicit formats through PTYs with `PAGER=cat`.
The executable name stays `openai` in both environments.

The page checker observes empty results, long IDs, escaped controls, unfamiliar fields, and errors.
It checks loaded chunks, cached back navigation, and terminal restoration.
It preserves rendered views, raw output, casts, key events, and request logs.
Successful final pages require `q`.
Errors require the existing nonzero status and preserved partial results.

Historical evidence applies only to its recorded source and binary identities.
Run the recipe to create evidence for another candidate.
Inspect the rendered images before reporting visual success.
These captures establish terminal replay, not native Terminal appearance or clipboard behavior.
