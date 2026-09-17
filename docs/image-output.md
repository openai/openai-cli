# Saving generated images

Run this checkout from a terminal:

```sh
go run ./cmd/openai images generate --prompt "A tiny orange robot painting a blue flower"
```

The command saves images in `~/Downloads/gpt-images/`, creates that folder if needed,
and prints each saved file's full path. Default names use local date and time,
such as `image-2026-09-17-093608.png`. Existing names get a `-2`, `-3`, etc.
suffix, so earlier images are never overwritten. PNG, JPEG, and WebP extensions
follow the returned image bytes.
When saving and neither a model nor a legacy `--response-format` is supplied,
this version uses `gpt-image-2.5-sunburst`. `--model` and models supplied through
JSON/YAML stdin take precedence. An explicit legacy response format preserves
the API's model selection.

Choose a meaningful name:

```sh
openai images generate --prompt "A tiny orange robot" --name orange-robot
```

This saves `orange-robot.png` (or the returned image format), then
`orange-robot-2.png` if the name is already taken. Supply a filename stem, not a
path or extension; use `--output-dir` for the folder. Names are checked before
generation. The CLI does not put prompt text into filenames automatically.

Choose another existing folder:

```sh
go run ./cmd/openai images generate \
  --prompt "A tiny orange robot painting a blue flower" \
  --output-dir "$HOME/Pictures"
```

An explicit `--output-dir` or `--name` also enables saving in scripts and other environments
without an interactive terminal. Custom folders must already exist; an invalid
folder is rejected before sending the generation request. `--output-format`
continues to select PNG, JPEG, or WebP encoding, whereas `--output-dir` chooses
the folder.

For JSON output:

```sh
go run ./cmd/openai --format json images generate \
  --model gpt-image-2.5-sunburst --prompt "A tiny orange robot"
```

Redirected or piped stdout retains its API output unless `--output-dir` or `--name` is supplied.
JSON/YAML piped into stdin can still produce saved images when stdout is a terminal.

An explicit output `--format` other than `auto`, transforms, raw output, streaming,
and `--response-format url` retain their API output. Combining these modes with
`--output-dir` or `--name` reports a conflict before making an API call.

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

If a preview font was deleted but the cached thumbnails remain, run:

```sh
openai images inline repair
```

Repair rebuilds the font while retaining each image's character mapping, then
enables it in this tab without selecting another profile. `setup` also repairs a missing font. Neither operation
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
saves the original before opening it and implies saving even with redirected
stdout. By default, `--open` replaces the inline preview; explicitly combining
`--open --inline on` requests both. Desktop windows open only when requested.
The saved image is never downscaled or rewritten for the external viewer.

The operating system chooses the viewer: macOS uses `open`, Windows uses its
shell file-opening API, and Linux desktop sessions use `xdg-open`. No new
terminal installation is required. The viewer runs on the machine executing
the CLI; SSH does not automatically open a file on your laptop. Headless Linux
reports a missing desktop before generation. If opening fails after a generation,
the image remains saved and the CLI provides a local retry command.

`--open` conflicts with explicit API output formats, transforms, streaming and
URL-only responses. Local viewer handoff validates PNG/JPEG/WebP contents and
filename extensions and passes the path directly to the OS without a shell.

Inline previews are **on by default** for interactive saving. iTerm2, Ghostty,
and Kitty display a real image thumbnail. The configured Apple Terminal profile
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

Redirected stdout and CI receive no preview. Explicit JSON and streaming keep
their existing output. tmux/screen/Zellij use text previews rather than graphics
passthrough. Piped stdin does not disable a terminal preview. Over SSH, a
recognized terminal identity enables native graphics; otherwise text is used.
Files remain on the machine running the CLI. No local-file access by the terminal
is needed.

Native protocol detection uses stdout's TTY status and known terminal identifiers. It is best
effort: terminal settings can disable image display. Preview detection never
reads replies from stdin. The opt-in Apple Terminal font path additionally checks
its owned profile through macOS automation. If a preview fails after generation,
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

The help starts with examples for the default folder, a custom folder, and JSON
output. Common options appear first, the prompt is marked required, and all API
options remain available. Model-specific limits are linked rather than repeated
in long flag descriptions. The maximum streaming-event count is unlimited unless
specified; omitted image sizes are selected by the model/API.

## Development workspace

This feature lives in the `openai-cli` checkout on branch `codex/images-save`.
The Go SDK already calls the image generation API. File saving and presentation
are CLI behavior; they do not require a new backend operation or a changed API
schema. Handwritten `image_help.go` supplies help text and the output-directory
flag without replacing generated API flag definitions or defaults. Policy lives
in `image_output.go`, local preview registration in `image_preview.go`, file handling in `internal/imageoutput`, and terminal
rendering in `internal/imagepreview`. The generated
`image.go` has only the request-options and response-saving integration points.
Normal generation preserves custom code through Castiron's existing merge
workflow. Generation and custom-code budget verification are release gates before
this prototype is proposed for shipping.

Preview decoding has a 32-megapixel budget (32 × 1024 × 1024 pixels) and a
16,384-pixel limit per axis. The axis limit also bounds the resize working buffers
for extremely thin images. Larger images are still saved in full and use the
saved-path fallback; API responses and saved-file size are not restricted.
The iTerm2 thumbnail is normalized to 8-bit PNG at up to 400 pixels per edge to
keep its OSC below 1 MiB; Kitty uses up to 1024 pixels with chunked transmission.
Neither protocol changes the original image.

Focused tests use synthetic responses and temporary directories:

```sh
go test ./internal/imagefont ./internal/imagefontmac ./internal/imagegallery ./internal/imageprefs ./internal/imageopen ./internal/imageoutput ./internal/imagepreview
go test ./pkg/cmd -run '^Test(ImageInline|ImagePreview|ImageOutput|ImagesGenerateOutput)' -count=1
```

These exercise the real CLI against a local HTTP server. A final real generation
uses `OPENAI_API_KEY` from your environment and confirms account/model access and
the hosted API response. Keep credentials out of commands, source files, and chat.
