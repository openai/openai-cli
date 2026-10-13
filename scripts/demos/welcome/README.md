# Welcome banner comparison

The recorder runs bare `openai` on real before and after binaries.
Color-capable UTF-8 terminals show a six-row OPENAI wordmark.
Each face pixel occupies two terminal columns. The 58-column face bitmap uses six gradual blue shades.
Truecolor and 256-color output add one-column right shadows, extending the artwork to at most 59 columns.
Literal OpenAI CLI text and the complete runtime version start at zero-based column 63.
Truecolor and 256-color output place the title two rows below the artwork's top edge.
The version follows directly below the title.
The card includes two columns of padding on each side and a blue border.
The colored card occupies nine rows and at least 76 columns.
Its width is `max(76, 66 + displayed version width)`, measured in terminal cells.
Versions wider than 16 cells use the compact text layout.
Color-capable UTF-8 terminals below the required artwork width also use the compact text layout.
They never show a clipped wordmark or substitute partial block glyphs.
The tagline is `What are we making today?`, centered beneath the artwork in the last interior row.
The bottom border contains the original `>_` cursor stamp, with two spaces on each side.
Apple Terminal uses a font-drawn bottom stroke to match the stamp's baseline.
A box-drawn end cap keeps that stroke inside the right corner. Other terminals retain their existing border.
The card contains no mascot or added outline.
In dark mode, the border uses `#5675BC`, matching the approved color swatch.
Underlined fractional blocks soften the top and bottom edges and balance the P/E/A bars.
Reverse masks inherit the terminal background, avoiding bright protruding ends.
The border and fractional blocks depend on font rendering; other faces and shadows use continuous background fills.
The approved ANSI fixture is `internal/clihelp/testdata/welcome-approved.ansi`.
It preserves the user-selected design for comparison with the actual renderer.
Native appearance outside the user's Apple Terminal preview still needs separate verification.
Literal identity inherits terminal foreground, and the version uses the existing muted tone.

Monochrome UTF-8 output retains the original three-row glyph wordmark at widths of 56 columns or more.
That fallback stays 56 columns by six rows. Its full and half-block glyphs remain font-rendered.
Other or unknown encodings retain the ASCII frame and literal-title fallback at those widths.
`LC_ALL` takes precedence over `LC_CTYPE` and `LANG`.
Windows Terminal advertises Unicode when no locale is set.
Classic Windows consoles without `TERM` or `WT_SESSION` use unstyled output.
Their unknown encoding selects the ASCII fallback without enabling console modes.
MSYS/Cygwin PTY pipe handles also qualify; ordinary pipes and files remain redirected streams.
When Windows cannot report a pipe terminal's dimensions, the existing help renderer uses its 80-column fallback.
The ASCII fallback escapes non-ASCII revision characters without losing their values.
Color selection remains independent from character encoding.
The 16-color fallback retains its single-color fills, earlier metadata positions, left-aligned tagline, and plain border.
`NO_COLOR`, `FORCE_COLOR=0`, and `CLICOLOR=0` disable styling.
The header performs no terminal query, starts no helper process, and requires no setup or image file.
Quiet mode, pipes, CI, and `TERM=dumb` preserve existing help without the card.
Font shapes and color rendering can still differ between terminals.
The compact text layout uses six rows from 40 columns, or four rows below 40 columns.
Terminals below 29 columns omit the optional card.
If the complete identity and version cannot fit, the CLI preserves help without a banner.
It uses the shared capture lifecycle and the existing rejecting loopback fixture.
It supplies no API credentials and requires zero API requests.
Temporary HOME, XDG directories, working directories, and PATH isolate each scene.
Existing first-run setup remains enabled within that temporary environment.

Use the repository's required Go version with `GOMAXPROCS=2` for these builds.
Acquire the coordinator's build and PTY slots before recording.

Build the existing fixture:

```sh
GOMAXPROCS=2 go build -p 2 -o dist/demos/bin/image-model-demo-api ./scripts/demos/image-models/main.go
```

The public comparison checker includes widths 75, 76, 81, and 82 around the artwork thresholds.

Build the baseline binary from a pinned current-main commit in its own checkout.
Set `BEFORE_SHA` to that exact commit and record it with the evidence.
Build the candidate binary from the reviewed candidate commit.
Verify both binary identities before supplying their full commit IDs.
The recorder records binary hashes but cannot establish their source provenance.

Run the comparison from the candidate checkout:

```sh
bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-color
```

The recorder needs `asciinema`, `agg`, `ffmpeg`, `ffprobe`, and `python3` on PATH.
The shared lifecycle requires a new, empty output directory outside the repository.
Use `DEMO_API_BINARY` to select a separately built copy of the existing fixture.
Set `DEMO_TERM_PROGRAM=Apple_Terminal` to record the CLI's Apple Terminal bottom-stroke correction.
The default is empty, which records the general-terminal border.
The recorder records this hint and checks the selected stroke and right-corner cap in the actual CLI output.
This selects the application output path; the recording remains a terminal replay, not an Apple Terminal screen capture.

Record the light background:

```sh
DEMO_THEME=light \
  bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-light
```

`DEMO_THEME` accepts `dark` or `light` and defaults to `dark`.
Dark captures use neutral `#171717` with foreground `#EEEEEE` and `COLORFGBG=15;0`.
Light captures use neutral `#FAFAFA` with foreground `#202020` and `COLORFGBG=0;15`.
The recorder declares its ANSI palette explicitly. The CLI does not set the terminal background.
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
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-no-color
```

Record the narrow fallback:

```sh
NO_COLOR=1 DEMO_COLUMNS=28 DEMO_ROWS=500 \
  bash scripts/demos/record-welcome.sh \
  "$BEFORE_BINARY" "$AFTER_BINARY" \
  "$BEFORE_SHA" "$AFTER_SHA" \
  /absolute/path/outside/repository/welcome-narrow
```

`DEMO_COLUMNS` defaults to 90, and `DEMO_ROWS` defaults to 250.
Both settings accept integers from 1 to 999.
The recorder rejects captures that would scroll the header out of view.
Increase `DEMO_ROWS` if the validation reports insufficient rows.
These tall captures preserve full help; they do not demonstrate ordinary viewport height.

The recorder checks the banner version against `openai --version`.
The comparison checker also accounts for the complete displayed version, including ASCII escapes, when deciding whether the card fits.
It verifies all six filled pixel rows, the nine-row card, and the complete metadata layout.
It separately checks the unchanged monochrome and ASCII fallback dimensions.
It compares complete help and command groups with the baseline after removing the banner.
Below the banner's required width, it requires exact baseline output.
With `NO_COLOR` set, it rejects terminal escapes in CLI output.

Full casts, transcripts, CLI output, screenshots, and GIFs remain in the output directory.
Stills use the complete replay's final frame so that burst output appears in the screenshots.
`validation.txt` records assertions, and `metadata.txt` records versions, hashes, dimensions, and exit statuses.
`before-top.png`, `after-top.png`, and `comparison-top.gif` show labeled top crops for review.
The crops show approximately `DEMO_PREVIEW_ROWS` rows, which defaults to 32.
That setting cannot exceed `DEMO_ROWS`.
The crops supplement full evidence and do not prove that all help fits an ordinary viewport.
Complete help normally scrolls the welcome card out of an 80×24 terminal's final viewport.
Review the final viewport and returned shell prompt as well as the opening crop.
The recording scene prints its return-prompt marker after the actual CLI exits.

Inspect both screenshots and the comparison GIF before sharing them.
Keep generated media outside Git.
These artifacts show Bash PTY replay through Menlo and the selected renderer theme.
The recorder uses Menlo 18px with agg's default line height of 1.4.
At this setting, the original `>` aligns with the border midpoint and `_` retains its lower baseline.
The previous 1.2 setting compressed replay rows and placed `>` below the border.
They do not establish native graphical terminal, Linux, Windows, or font behavior.

Use DEMO_ENCODING=ascii to exercise the ASCII frame and literal-title fallback.
The default DEMO_ENCODING=utf8 selects the filled wordmark when color and width permit.
With NO_COLOR, UTF-8 captures retain the original glyph wordmark.
The superseded mascot experiments and their recordings remain outside Git as historical evidence.
