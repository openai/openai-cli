# Welcome banner comparison

The recorder runs bare `openai` on real before and after binaries.
It uses the shared capture lifecycle and the existing rejecting loopback fixture.
It supplies no API credentials and requires zero API requests.
Temporary HOME, XDG directories, working directories, and PATH isolate each scene.
Existing first-run setup remains enabled within that temporary environment.

Use Go 1.26.9 with `GOMAXPROCS=2` for these builds.
Acquire the coordinator's build and PTY slots before recording.

Build the existing fixture:

```sh
GOMAXPROCS=2 go build -p 2 -o dist/demos/bin/image-model-demo-api ./scripts/demos/image-models/main.go
```

Build the baseline binary from `217ff2ea1e853040ba08c3c170e05586424386aa` in its own checkout.
Build the candidate binary from the reviewed candidate commit.
Verify both binary identities before supplying their full commit IDs.
The recorder records binary hashes but cannot establish their source provenance.

Run the comparison from the candidate checkout:

```sh
bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  217ff2ea1e853040ba08c3c170e05586424386aa "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-color
```

The recorder needs `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` on PATH.
The shared lifecycle requires a new, empty output directory outside the repository.
Use `DEMO_API_BINARY` to select a separately built copy of the existing fixture.

Record the light background:

```sh
DEMO_THEME=light \
  bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  217ff2ea1e853040ba08c3c170e05586424386aa "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-light
```

`DEMO_THEME` accepts `dark` or `light` and defaults to `dark`.
Dark captures use Dracula rendering and `COLORFGBG=15;0`.
Light captures use GitHub Light rendering and `COLORFGBG=0;15`.
Set `COLORFGBG` explicitly to override the application background hint.
`COLORTERM` defaults to `truecolor`; set it explicitly to test another advertised color capability.
The PTY always uses `TERM=xterm-256color`.
`NO_COLOR` suppresses application styling with either renderer theme.
Metadata records these exact color settings.

Record the 40-column monochrome case:

```sh
NO_COLOR=1 DEMO_COLUMNS=40 DEMO_ROWS=350 \
  bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  217ff2ea1e853040ba08c3c170e05586424386aa "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-no-color
```

Record the narrow fallback:

```sh
NO_COLOR=1 DEMO_COLUMNS=28 DEMO_ROWS=500 \
  bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  217ff2ea1e853040ba08c3c170e05586424386aa "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-narrow
```

`DEMO_COLUMNS` defaults to 90, and `DEMO_ROWS` defaults to 250.
Both settings accept integers from 1 to 999.
The recorder rejects captures that would scroll the header out of view.
Increase `DEMO_ROWS` if the validation reports insufficient rows.
These tall captures preserve full help; they do not demonstrate ordinary viewport height.

The recorder checks the banner version against `openai --version`.
It compares complete help and command groups with the baseline after removing the banner.
Below the banner's required width, it requires exact baseline output.
With `NO_COLOR` set, it rejects terminal escapes in CLI output.

Full casts, transcripts, CLI output, screenshots, and GIFs remain in the output directory.
`validation.txt` records assertions, and `metadata.txt` records versions, hashes, dimensions, and exit statuses.
`before-top.png`, `after-top.png`, and `comparison-top.gif` show labeled top crops for review.
The crops show approximately `DEMO_PREVIEW_ROWS` rows, which defaults to 32.
That setting cannot exceed `DEMO_ROWS`.
The crops supplement full evidence and do not prove that all help fits an ordinary viewport.

Inspect both screenshots and the comparison GIF before sharing them.
Keep generated media outside Git.
These artifacts show Bash PTY replay through Menlo and the selected renderer theme.
They do not establish native graphical terminal, Linux, Windows, or font behavior.
