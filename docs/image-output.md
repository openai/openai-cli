# Saving images

## Getting started and finding help

Run `openai` with no arguments for a short starting guide. It appears when you
ask for the CLI's help, not when you open a shell or run an API request.

```sh
openai                              # Starting guide
openai help setup                   # How to enter your API key
openai images generate --help       # Common controls and defaults
openai images options               # All everyday settings, in plain language
openai images options quality       # Choices and an example for one setting
openai images options quality --all # Longer explanation and compatibility details
openai images models                # Exact image model IDs and visibility checks
openai help --all images generate   # Every image option and global flag
openai help --all                   # All commands and global flags
```

If you built this checkout with `go build -o openai ./cmd/openai`, use `./openai`
instead of `openai`. The starting guide uses that same invocation in its examples.
Help does not require an API key. Generation uses `OPENAI_API_KEY` from your
environment.

The welcome guide follows the first image workflow: set up your key, describe
an image, see where it is saved, then find the controls to change the result.
The setup page provides hidden-input instructions for Bash/zsh and PowerShell;
it only displays instructions and never prompts for, stores, checks, or prints
your existing key. Those instructions set the key for the current shell session.
Short image help shows one starting command, the current model and image preset,
and common adjustments: name the file, choose a folder/model/count, open it in a
separate window, or hide the preview. It also explains that redirected output
still saves images and scripts must add `--format json` for API data.
`help --all images generate` contains defaults, saving/viewing behavior,
scripting rules, examples, every flag, and the complete generated API descriptions
(including model limits). Full-help examples preserve the `./openai` invocation
too. Nothing needs to be looked up online just to recover the previous flag help.
Mistyped image options get a short correction and help link instead of the long
reference. A description supplied without `--prompt` gets a copyable example;
the error does not echo the description.

## When something goes wrong

Interactive image saving gives a short explanation and a next step for common
API failures: missing or rejected keys, access restrictions, invalid image
settings, rate limits, quota/billing limits, timeouts and service errors.
For example, a missing key points to `./openai help setup` when running this
checkout. A quota failure points to billing/limits instead of advising a wait.
These messages do not echo credentials, prompts or arbitrary server text.

Errors go to stderr as readable text, including in scripts. Use
`--format-error json` for the complete API error. `--format json` also selects
JSON errors unless `--format-error` overrides it. Exit codes, authentication and
the SDK's retry policy are unchanged. This presentation layer never switches
models or retries a request itself.

Missing prompt and invalid output-folder errors point to usable commands or the
automatic Downloads folder. `images preview --help` gives a short local-viewing
guide, and missing files, folders, filenames with spaces and unsupported image
formats get specific guidance. Previewing an existing file makes no API request.

## The default preset

Run this checkout:

```sh
go run ./cmd/openai images generate --prompt "A tiny orange robot painting a blue flower"
```

Interactive saving first prints `Generating image...` (or `Generating images...`
for a batch) to stderr, so you know the command is working. This is a waiting
message, not a percentage or an estimate. It appears after local validation and
setup, before the request. Explicit formats/debug, CI and redirected output do
not receive this message.

The command saves images in `~/Downloads/gpt-images/`, creates that folder automatically,
and prints each saved file's full path, including when output is piped or redirected.
Default names come from the prompt:
`A tiny orange robot` becomes `tiny-orange-robot.png`. Existing names get a `-2`, `-3`, etc.
suffix, so earlier images are never overwritten. PNG, JPEG, and WebP extensions
follow the returned image bytes.
When saving and neither a model nor a legacy `--response-format` is supplied,
this version uses `gpt-image-2.5-sunburst`. `--model` and models supplied through
JSON/YAML stdin take precedence. An explicit legacy response format preserves
the API's model selection. The default is the current Sunburst model family;
the CLI does not query the model catalog or switch models after an error.
See the [official model documentation](https://developers.openai.com/api/docs/models/gpt-image-2.5-sunburst).
For another model, use an explicit name, for example `--model gpt-image-2.5-flare`.
Image help links to `openai images models` for exact image model IDs. Model
selection uses exact IDs, with no `fast`/`best` labels.

### Find an image model

```sh
openai images models                  # Check known image model IDs with your key
openai images models --all            # Also show known snapshots and hidden rows
openai images models --offline        # Known names, with no API call or key
openai --format json images models    # Structured results for scripts
```

The readable view is a compact table with the CLI default marked and a
copyable generation example. It returns directly to the shell; there is no pager
to exit, JSON metadata to inspect, or filtering pipeline to remember. Retired
models and IDs not visible to the key are hidden from the default table with a
count and `--all` instruction. The JSON report retains all checked rows.

Discovery checks a maintained catalog of exact image IDs from the installed Go
SDK. It retrieves each model individually instead of requesting the large
all-model catalog, so it can still work when `models list` times out. It may miss
newly released or account-specific image models; explicit `--model` values still
pass through without requiring catalog membership. The existing `models list`
operation remains available for the full account catalog.

`Visible` means the current credentials can retrieve model metadata. Generation
permissions, option compatibility and quota are separate. A 404 is not visible;
an announced shutdown on or before today's UTC date is retired. Authentication,
permission failures, rate limits, invalid responses, network errors and timeouts
remain **unknown**, never an empty list of available models. The CLI displays
partial results, gives a concise next step, and exits nonzero on unknown checks.

Only metadata GETs are sent: at most three concurrently, with a five-second
deadline per request and a fifteen-second deadline for the discovery operation.
Checks do not retry automatically; authentication rejection or rate limiting
stops queued checks. This command does not generate images or change image
generation's retry policy. Offline mode is explicitly marked unchecked and makes
no requests. No credentials, account results or access decisions are cached.

Explicit `--format json` output includes `source`
(`live` or `offline`), `default_model`, `complete`, and `models`. Each row includes
the exact `id`, `snapshot`, `default`, `status`, and optional safe `failure` and
`shutdown_date`. `complete` means all requested live checks resolved, including
not-visible/retired results; it is false for offline or incomplete checks. Offline
mode intentionally exits zero. In JSON mode, partial live failures preserve valid
JSON on stdout and print a concise explanation on stderr. Both `images models`
and `models list` use readable output by default, including in scripts; add
`--format json` when parsing their results.

### Generation defaults and saving

When the CLI selects its default image model, it fills in these omitted settings:

| Setting | Default | Change it with |
| --- | --- | --- |
| Model | `gpt-image-2.5-sunburst` | `--model MODEL` |
| Number of images | 1 | `--count COUNT` (or `-n COUNT`) |
| Dimensions | Automatic | `--size WIDTHxHEIGHT` |
| Quality | Automatic | `--quality LEVEL` |
| File format | PNG | `--output-format png\|jpeg\|webp` |
| Background | Automatic | `--background auto\|transparent\|opaque` |
| Moderation | Automatic | `--moderation auto\|low` |
| Partial images | None (0) | `--partial-images 0\|1\|2\|3` |
| Save folder | `~/Downloads/gpt-images/` | `--output-dir DIRECTORY` |
| Inline preview | On initially | `--inline on\|off` |

Flags and values supplied through stdin override the preset, including explicit
nulls. An explicitly selected model uses that model's API defaults for omitted
settings. The preset explicitly sends background/moderation `auto`,
`partial_images: 0`, and `stream: false`. Requesting progress previews enables
streaming automatically when saving. Legacy model style and JPEG/WebP
compression remain optional. Settings can be changed per command; only the
`images inline on` / `off` commands save a preview preference.

### Choose a setting without reading the full API reference

`openai images options` shows a short directory of controls. Each topic starts
with one example, the choices/default and essential limits, in at most twelve
lines. Add `--all` to see its complete explanation, additional examples and
script behavior. The longer information has been retained:

```sh
openai images options model
openai images options size
openai images options quality
openai images options count
openai images options format
openai images options background
openai images options moderation
openai images options partials
openai images options upload
openai images options save
```

These are local help pages: they do not generate images or check credentials.
The example generation and editing commands do make API requests when run.
`--count` is a readable alias for both `-n` and `--n`:

```sh
openai images generate --prompt "A tiny orange robot" --count 10
openai images generate --prompt "A tiny orange robot" --size 1024x1536 --quality high
openai images generate --prompt "A tiny orange robot sticker" --background transparent
```

Count must be 1–10; DALL-E 3 and streaming support one final image per request.
Partial count must be 0–3, and transparent backgrounds require PNG or WebP.
The CLI validates these combinations before generation, using the final merged
flags and JSON/YAML input. It continues to accept exact model IDs and future
quality/size values without requiring membership in a hardcoded enum.

### Edit an image or make a variation

```sh
openai images edit --image "photo.png" --prompt "Make the sky purple"
openai images create-variation --image "square-photo.png"
```

Both commands save new images automatically, including when redirected, and keep
your source files unchanged. They share generation's `--name`, `--output-dir`,
`--count`, `--open` and `--inline` controls, collision protection, and previews.
Use `--help` for a short guide or `help --all images edit` (or
`create-variation`) for every generated API option.

Editing uses `gpt-image-2.5-sunburst` by default when saving and names the result
from your prompt. Repeat `--image` for additional references or supply `--mask`
to limit edits. Progress previews work with `--partial-images 1`, `2` or `3`;
only the completed edit is saved. Editing does not send the generation-only
`moderation` preset.

Variations use the endpoint's supported `dall-e-2` model, require a square PNG
under 4 MB, and use `image-variation.png` as their automatic filename (with
collision suffixes). Variations do not support streaming or progress previews.

Add `--format json` for full API data without saving. Explicit data formats,
`--transform`, `--raw-output` and legacy `--response-format url` keep their API
behavior. The shared multipart encoder streams uploads without rereading input
files or stdin; the original files are never replaced by the saved results.

### Progress previews while generating

```sh
openai images generate --prompt "A tiny orange robot" --partial-images 2
```

This enables streaming automatically and shows up to two progress previews
when terminal previews are enabled and supported. Only the
finished image is saved in the selected output folder. Partial files use a
private temporary directory and are cleaned up. Previews may arrive fewer times
than requested if the final image is ready sooner. Partial images add API usage;
`--inline off` hides previews but does not remove that usage. Zero partials is the
default and does not hide the finished-image preview.

Progress previews require one final image per request (`--count 1`). An explicit
false/null streaming value with positive partials is rejected rather than
silently overwritten. An event limit (`--max-items`) cannot truncate a saving
workflow before the final image. If a stream ends without a final image, the CLI
reports that no final image was received instead of reporting an empty success.

`--stream true` also saves the final image automatically, including in scripts.
Explicit API data formats/transforms retain API-event output and require
`--stream true` when requesting partials. For complete JSON events, choose a
format and model explicitly:

```sh
openai --format json images generate --prompt "A tiny orange robot" \
  --model gpt-image-2.5-sunburst --stream true --partial-images 2
```

The generated full reference remains available for all API parameters and model
limits. See the [official image-generation guide](https://developers.openai.com/api/docs/guides/image-generation).

### Names and folders

Choose a meaningful name:

```sh
openai images generate --prompt "A tiny orange robot" --name orange-robot
```

This saves `orange-robot.png` (or the returned image format), then
`orange-robot-2.png` if the name is already taken. You may supply `orange-robot`
or `orange-robot.png`; a recognized PNG/JPEG/JPG/WebP extension is removed before
the actual image format supplies its extension. The name does not select a
format; use `--output-format` for that and `--output-dir` for the folder.
Names and filesystem filename constraints are checked before generation. The
CLI creates a short name locally from the prompt; naming makes no extra API call.
It keeps up to eight words within 80 UTF-8 bytes, removes an initial a/an/the,
and replaces unsafe filename characters with separators. Unicode words are
preserved. Prompts without usable text fall back to a dated filename. Explicit
`--name` values take priority. Generation still receives your original prompt.

For a batch, every complete image is kept even if another image cannot be saved.
The CLI prints the completed paths, reports how many were saved, and exits with
an error. It removes incomplete files only and does not regenerate anything.
Cancellation stops further saving while preserving files already completed.

Choose another existing folder:

```sh
go run ./cmd/openai images generate \
  --prompt "A tiny orange robot painting a blue flower" \
  --output-dir "~/Downloads"
```

Saving works in scripts without extra flags. Use `--output-dir` or `--name` to
choose the folder or filename. Custom folders must already exist; an invalid
folder is rejected before sending the generation request. `--output-format`
continues to select PNG, JPEG, or WebP encoding, whereas `--output-dir` chooses
the folder.

For JSON output:

```sh
go run ./cmd/openai --format json images generate \
  --model gpt-image-2.5-sunburst --prompt "A tiny orange robot"
```

Piped or redirected stdout contains readable saved-file information by default.
**Scripts that previously expected JSON from redirected output must add
`--format json`.** This is an intentional change to the default. JSON/YAML input
can still be piped into stdin while images save automatically.

`--format auto` and `--format text` keep automatic saving. Explicit `json`,
`jsonl`, `raw`, `yaml`, `pretty`, and `explore` formats, transforms, raw output,
and `--response-format url` select API results instead. Combining these modes
with `--output-dir`, `--name`, or `--open` reports a conflict before an API call.
`--format json` includes the full response and encoded image data.

`--response-format b64_json` can still save images; it is a legacy API parameter,
distinct from the CLI's `--format json` output option. Explicit DALL-E models use
`b64_json` when saving if no response format was specified, so this feature does
not need to download signed URLs. Image editing remains a separate follow-up.

## Inline previews

For a sharp image displayed inside the terminal, run the command in **Ghostty,
iTerm2, or Kitty**. These terminals receive actual PNG image data. Modern Apple
Terminal's full RGB support improves the text fallback's colors, but that path
still represents the image with characters and remains visibly lower detail.

An experimental Apple Terminal setup can also show actual bitmap pixels using
color font glyphs; see below. It keeps your chosen profile and needs local macOS setup.

No new generation is needed to compare terminals: open a supported terminal and
run `openai images preview FILE` on the same saved image. The CLI detects the
terminal automatically; there is no image-quality flag to enable.

### Sharp images in Apple Terminal (experimental)

```sh
openai images inline setup
```

Setup keeps your **text typeface, style, point size, selected Inspector profile**,
colors, background, ANSI palette and spacing, then displays a built-in sample. It does
not import a profile, select a preset, change saved settings, or open another
window. Reselect your profile in **Shell → Show Inspector → Profile** to restore
its original font selection. The active font has a generated internal name, but
uses the original font's outlines, metrics and available bold/italic faces.
Source font files are never changed or distributed. If upgrading from an older
preview font, first select your preferred font and size in Inspector: the old
version did not record your original typography, so it cannot restore it reliably.

To repeat the visual check:

```sh
openai images inline test
```

The built-in sample checks smooth colors, tile edges, and normal text spacing.
It reports setup errors directly instead of falling back to a text approximation.
Repeating the test reuses the cached sample.
After the sample looks clear, use `openai images preview FILE` or your usual
`openai images generate --prompt "..."` command in that window.
No additional app, API call, or API key is needed for setup or local previews.
Setup and repair always use the current tab's selected settings. They do not
create or export Terminal profiles, and have no separate-window or preset mode.
The private font copy is managed internally; there is no new profile to select.

Interactive `images generate` and `images preview` also offer setup when your
Apple Terminal tab uses an ordinary font. The prompt explains the font change
and defaults to no. Accepting enables the image font in that tab;
declining keeps the text preview and does not change your saved on/off setting.
Once the tab is ready, later image commands do not ask again. A new ordinary tab
may ask to enable its font. The prompt requires matching real input/output
terminals: piped input, redirected output, JSON, streaming, CI, SSH and multiplexers
never consume a setup answer. iTerm2/Ghostty/Kitty continue using native images.

Setup or the first preview may request macOS **Automation** permission to control
Terminal. This lets the CLI select the image-capable font copy in your consenting
tab's temporary settings, addressing it by its exact TTY. It does not modify
saved profiles, your default profile, or other tabs, and it does not execute
shell commands through Terminal. The usual `--inline off` flag
skips previews and automation.
Generation checks the enabled tab before making the API request; a later
preview failure still leaves the generated image saved.

The renderer encodes each image row as one PNG strip in a private color font,
eliminating internal vertical tile boundaries without overlapping transparent
pixels. The remaining character cells reserve the same width. Bitmap strikes
are built for the selected point size and measured cells, at 2× and 4× resolution.
Normal shell characters retain the installed font's original outlines and tables.
Image characters use a separate supplementary private-use range, leaving existing
prompt icons untouched. Each new image receives new
characters, and subsequent fonts retain older images to preserve scrollback.
Previewing the same image reuses its characters and does not call the API.

For custom character/line spacing, the renderer measures Terminal's character
grid through the terminal device and fits its bitmap strips to those dimensions.
Separate immutable font variants allow tabs with different spacing to coexist.
Every variant keeps the same image character assignments and fits the original
preview within its existing grid, preserving aspect ratio. Changing spacing can
change that fit; previews in other tabs keep their own font. No spacing setting
is rewritten.

Practical limits of this experimental renderer:

- Setup retains whole-number point sizes supported by Terminal's scripting API;
  it never substitutes 16pt. Very large previews can exceed the bitmap budget,
  in which case the command reports an error without selecting a different size.
- Static TrueType, name-keyed CFF1, and supported named TrueType variations are
  supported, including Terminal's static SF Mono and variable SF Mono Terminal.
  Exact native display names are accepted; bundled source fonts are read without
  installing or registering them. Selected variation coordinates and original
  glyph outlines are retained. Unsupported outline/variation/color-font formats
  fail before selecting a replacement; no fallback typeface is imposed.
  Native checks cover SF Mono Terminal's Regular/Bold/Italic family, including
  matching Retina text samples and style switching. Some other variable weights
  can select different fallback fonts; non-Retina antialiasing can also differ.
  Every installed third-party font and display configuration has not been
  visually verified.
- Selecting another ordinary profile/font afterward replaces the image-capable
  copy. Run setup to enable that tab with the newly selected text style and size.
- Old prototype previews using BMP private characters need to be displayed
  again after this upgrade. New previews preserve their supplementary mappings
  across font/style variants and repeated commands.
- Custom spacing adapts when Terminal reports a uniquely measurable grid. If
  the window is too small or measurements are inconsistent, enlarge it and
  retry. Older environments reporting no extents retain standard tile geometry.
- New previews fit the current window width. Narrowing the window later can
  wrap earlier image rows. Widen it to the size reported by `status --check`;
  repeating a cached preview keeps its original width and character mapping.
- The private gallery has 6,400 image cells (about 12 square images at the
  default size). It reports when full instead of replacing earlier images.
- Font files and reduced thumbnails contain image pixels. They are stored
  privately under the macOS user cache, with no prompts or original paths in
  gallery metadata. Original saved images are never modified.
- Fonts are registered for the login session. After logout, rerun setup in the
  tab you want to use. Restoring a Terminal scrollback session depends on
  the retained fonts and is not guaranteed across cache deletion or profile reset.
- This path works locally in Apple Terminal. SSH, multiplexers, redirected
  output, and CI do not use it. iTerm2/Ghostty/Kitty keep their native protocols.

Inspect the gallery with `openai images inline status`. It shows the saved
on/off preference, cached image count, approximate remaining square previews,
and disk usage. Add `--details` for the cache path and exact cell count.
`status --check` verifies the current tab's image font, supported font size, and
window width. `inline test` adds a visual check. Readiness follows the owned
image font, regardless of the tab's profile name. The status label **Image gallery**
identifies cached images; it is not the name of your selected Inspector profile.

If the base preview font was deleted but the cached thumbnails remain, run:

```sh
openai images inline repair
```

Repair rebuilds the base font while retaining each image's character mapping,
then enables it in this tab without selecting another profile. `setup` also
repairs a missing base font. If the selected typography variant was deleted,
first select your original text font in Terminal's Inspector, then run repair;
the missing variant cannot be inspected automatically. Neither operation
restores missing thumbnails; in that case, reset the previews and use the saved
originals again. Missing or damaged ownership metadata is reported rather than
guessing which files to delete.

To remove preview data,
close tabs using image-capable fonts and run `openai images inline reset` from an
ordinary Apple Terminal window. Reset unregisters the owned fonts and deletes
the owned preview cache, including when some cache files are already missing.
It keeps original image files and your saved on/off preference; old font-based image
scrollback will no longer display. Run setup again for a new gallery.

Earlier prototypes created `OpenAI Images` profiles. To remove an old prototype
profile, first switch its tabs to your preferred ordinary profile. Then open
**Terminal → Settings → Profiles**, select that prototype profile and click
**−**. Current setup does not recreate it. Removing a profile does not delete
the saved images.

### Full resolution from any desktop terminal

Use your existing desktop image viewer from Apple Terminal or another terminal
without native images:

```sh
openai images preview --open "$HOME/Downloads/gpt-images/orange-robot.png"
openai images generate --prompt "A tiny orange robot" --open
```

`preview --open` makes no API request and needs no API key. `generate --open`
saves the original before opening it, including with redirected stdout.
By default, `--open` replaces the inline preview; explicitly combining
`--open --inline on` requests both. Desktop windows open only when requested.
The saved image is never downscaled or rewritten for the external viewer.

The operating system chooses the viewer: macOS uses `open`, Windows uses its
shell file-opening API, and Linux desktop sessions use `xdg-open`. No new
terminal installation is required. The viewer runs on the machine executing
the CLI; SSH does not automatically open a file on your laptop. Headless Linux
reports a missing desktop before generation. If opening fails after a generation,
the image remains saved and the CLI provides a local retry command.

`--open` conflicts with explicit API output formats, transforms and
URL-only responses. Local viewer handoff validates PNG/JPEG/WebP contents and
filename extensions and passes the path directly to the OS without a shell.

Inline previews are **on by default** for interactive saving. iTerm2, Ghostty,
and Kitty display a real image thumbnail. An enabled Apple Terminal tab
uses the color-font renderer above. Other terminals, including unconfigured
Apple Terminal windows, display a text approximation using fine block shapes. Terminals that
advertise full RGB color use it; Apple Terminal 2.15/build 465 and newer also use
RGB automatically, including dotted build numbers such as `470.2`. Older Apple
Terminal versions use the 256-color palette. Basic terminals use ASCII. `NO_COLOR`,
`CLICOLOR=0`, and `TERM=dumb` select plain ASCII for the text fallback.

The full-resolution PNG/JPEG/WebP is saved first and never rewritten.
The preview fits the terminal, preserves proportions within character-cell
resolution, and leaves the cursor below it. Text thumbnails cannot reproduce
the detail of a native image display; open the saved file for full detail.
Text previews filter the original image directly and fit fractional block shapes
to 8×8 samples per cell, choosing two colors to preserve edges. Smooth regions
in the 256-color fallback can use shaded characters to reduce color banding.
Each cell still contains one character and two colors; these samples are not
independent display pixels. RGB removes palette banding, while character-cell
resolution remains a limit.

The techniques follow the approaches described by
[TerminalImageViewer](https://github.com/stefanhaustein/TerminalImageViewer#terminal-image-viewer-tiv)
and [Chafa](https://hpjansson.org/chafa/man/). Apple documents its newer
[24-bit Terminal color support](https://www.apple.com/my/os/pdf/All_New_Features_macOS_Tahoe_Sept_2025.pdf).

Preview an existing file without generating or saving another image:

```sh
openai images preview "$HOME/Downloads/gpt-images/orange-robot.png"
```

This local command needs no API key and makes no API request. It uses the same
native-image or text renderer as generation and requires terminal stdout.

To save without a preview:

```sh
openai images generate --prompt "A tiny orange robot" --inline off
```

Remember your preference for future generations:

```sh
openai images inline off
openai images inline on
```

These commands work on any supported OS, need no API key, and store the setting
privately in the user configuration directory, separately from the preview cache.
`--inline on` or `--inline off` overrides it for one generation. The explicit
`images preview FILE` command still shows an image when automatic previews are off.
The earlier `--no-preview` opt-out remains compatible but is hidden from help.

Redirected stdout and CI receive no preview. Explicit JSON keeps full API output;
ordinary streaming saves the final image. tmux/screen/Zellij use text previews
rather than graphics passthrough. Piped stdin does not disable a terminal preview. Over SSH, a
recognized terminal identity enables native graphics; otherwise text is used.
Files remain on the machine running the CLI. No local-file access by the terminal
is needed.

Native protocol detection uses stdout's TTY status and known terminal identifiers. It is best
effort: terminal settings can disable image display. Preview detection never
reads replies from stdin. The opt-in Apple Terminal font path additionally checks
the intended tab's font and settings through macOS automation. If a preview fails after generation,
the saved file remains available and the command prints local recovery guidance.
A failed explicit `images preview` or `images inline test` exits unsuccessfully
so that it cannot report success while showing no image.
Kitty/Ghostty use the current terminal pixel geometry when available. Otherwise
the width is bounded and the vertical footprint is estimated; image proportions
are always preserved.

Rendering follows the [iTerm2 inline-image protocol](https://iterm2.com/documentation-images.html)
and [Kitty graphics protocol](https://sw.kovidgoyal.net/kitty/graphics-protocol/),
which [Ghostty supports](https://ghostty.org/docs/features).

## Command help

```sh
go run ./cmd/openai images generate --help
```

The short help starts with one runnable example. Full help explains the CLI saving
preset separately from API defaults, then shows the original API flag descriptions
with their model-specific limits. Generated documentation is retained automatically;
CLI notes add context without replacing it. This includes future generated flags.
The maximum streaming-event count is unlimited unless specified; this counts
emitted events and is not a limit on generated images or cost.

## Development workspace

The [feature implementation map](image-implementation.md) lists the integration
files and libraries for each feature, with request and font-rendering diagrams.
The [output architecture](architecture/output-pipeline.md) explains how those
features use the existing generated hooks from PR #223 and why the entrypoint
changes are needed.

Generated handlers still make API calls. The custom layer coordinates commands;
`internal/imageoutput` owns saving and `internal/terminalimage` owns previews.
Explicit API output choices are preserved. Readable output and automatic saving
are now the default even in scripts; see the [output guide](readable-output.md)
for migration examples. Generation and custom-code budget verification remain
release gates before shipping.

Preview decoding has a 32-megapixel budget (32 × 1024 × 1024 pixels) and a
16,384-pixel limit per axis. The axis limit also bounds the resize working buffers
for extremely thin images. Larger images are still saved in full and use the
saved-path fallback; API responses and saved-file size are not restricted.
The iTerm2 thumbnail is normalized to 8-bit PNG at up to 400 pixels per edge to
keep its OSC below 1 MiB; Kitty uses up to 1024 pixels with chunked transmission.
Neither protocol changes the original image.

Focused tests use synthetic responses and temporary directories:

```sh
go test ./internal/imagefont ./internal/imagefontmac ./internal/imagegallery ./internal/imageprefs ./internal/imageopen ./internal/imageoutput ./internal/terminalimage
go test ./pkg/transformers ./pkg/custom -run '^Test(Image|ImagesGenerate|ReportImagePreview)' -count=1
go test ./cmd/openai -count=1
```

The process-level CLI tests live alongside the executable in `cmd/openai` and
are included by `go test ./...`. These checks exercise the actual entrypoint
against local HTTP servers. A final real generation uses `OPENAI_API_KEY` from
your environment and confirms account/model
access and the hosted API response. Keep credentials out of commands, source
files, and chat.
