# Image generation and saving

```sh
openai images generate --prompt "A tiny orange robot"
openai images generate --prompt "A tiny orange robot" --name robot --count 2
openai images generate --prompt "A tiny orange robot" --output-dir "~/Downloads"
openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare
openai images generate --prompt "A tiny orange robot" --output-format webp
openai --format json images generate --prompt "A tiny orange robot" --model gpt-image-2.5-sunburst
```

Generation uses API credits and requires access to the chosen model. The CLI's
default for saving is the exact ID `gpt-image-2.5-sunburst`. This is a preset,
not a guarantee of access or quota. When neither `model` nor `response_format`
is supplied, it requests one PNG, automatic size, quality, background and
moderation, no partial images, and no streaming. Flags and merged JSON/YAML
stdin override individual defaults, including explicit nulls. An explicit
model retains API defaults for omitted settings. Explicit `dall-e-2` and
`dall-e-3` requests use `b64_json` when the response format is omitted.

The default folder, `~/Downloads/gpt-images/`, is created when needed.
`--output-dir` must already exist and be writable. Prompt-derived names use up
to eight words and 80 UTF-8 bytes; prompts without usable text use a timestamp.
`--name` accepts a filename, with an optional image extension, but no path.
Existing filenames get suffixes such as `-2`. Before requesting images, the CLI
checks that the name leaves room for numbering and the longest image extension
on the output filesystem. If it is too long, choose a shorter `--name`;
explicit names are not truncated. PNG, JPEG and WebP container
signatures determine extensions; image bytes are preserved without re-encoding.
Every completed path is printed. A failed batch keeps completed files and
removes only its incomplete files. Check the output folder before regenerating.

Automatic and text output save on terminals and in pipes. Explicit data formats
(`json`, `jsonl`, `yaml`, `raw`, `pretty`, `explore`), `--transform`,
`--raw-output`, and `--response-format url` bypass saving and CLI defaults.
These options cannot be combined with `--name` or `--output-dir`. URLs are
returned as API data and are never downloaded. `--output-format` chooses the
image encoding; it is separate from the CLI's `--format`.

`--stream true` saves only the completed image. Positive `--partial-images`
selects streaming when saving and shows temporary progress previews. An explicit
false or null stream conflicts with positive partials. Streaming supports one
final image, and `--max-items` cannot truncate a saving stream. For API events,
use an explicit data format and `--stream true`. An incomplete stream fails
without claiming that an image was saved.

## Editing and variations

```sh
openai images edit --image photo.png --prompt "Make the sky purple"
openai images edit --image first.png --image second.png --mask mask.png --prompt "Add a purple sky"
openai images edit --image photo.png --prompt "Make the sky purple" --name result --count 2
openai images edit --image photo.png --prompt "Make the sky purple" --stream true
openai images create-variation --image photo.png
openai --format json images edit --image photo.png --prompt "Make the sky purple"
```

These existing commands now use the same saving folder, filename overrides,
collision protection and explicit-format opt-outs as generation. Source images
and masks are uploaded without being rewritten; saved results are new files.
Editing names come from the prompt. Variations use `image-variation`.

Saving edits uses the generation preset above without `moderation`, which the
edit endpoint does not accept. Variations default to `dall-e-2` and `b64_json`;
the endpoint requires a square PNG under 4 MB. Explicit models and nulls keep
the existing request semantics. In multipart requests, explicit null fields
retain their existing empty form-part encoding rather than acquiring defaults.

Streamed edits save only the final image. `--partial-images 1`, `2` or `3`
enables streaming and displays progress previews where supported.
Variations do not support streaming. Explicit `--format json` preserves the
original responses or edit events and makes no saved files. Model-discovery
default markers remain separate work.
Use `openai help --all images edit` or `openai help --all images create-variation`
for every request setting.

## Inline previews

Request progress while an image is generated or edited:

```sh
openai images generate --prompt "A tiny orange robot" --partial-images 2
openai images edit --image "photo.png" --prompt "Make the sky purple" --partial-images 2
# Allow sharp progress and final previews in a local Apple Terminal tab.
openai images generate --prompt "A tiny orange robot" --partial-images 2 --inline on
```

The existing `--partial-images` flag now displays up to the requested number of
intermediate images. Only the completed image is saved to the output folder.
Progress images decode in memory. Duplicate or out-of-range preview indexes are
ignored. An unavailable progress preview prints one notice beside the progress
on stdout and the CLI continues waiting for the final image. Stderr remains
available for structured errors. Cancellation, stream errors, output failures
and failed font restoration still stop the command.

Progress uses the same terminal capability as the final preview. Kitty/iTerm
use native graphics. In local Apple Terminal, `--inline on` or a saved
`openai images inline on` preference allows sharp image-font previews, preserving
ordinary text and the tab's font size and profile. `auto` retains the color
approximation.

Sharp progress previews keep private cached images and immutable font entries
so later previews do not replace pictures already in scrollback. These are
preview caches, not generated files in the output folder. Before adding a new
progress entry, the CLI leaves capacity for a final image in the current font.
If sharp preparation fails or would consume that capacity, one notice is shown
and the CLI waits for the final image instead of substituting blocky progress.
Font changes or another command can still affect final-preview availability;
the final image is saved even when its optional preview is unavailable.

`--inline off`, pipes and CI skip progress rendering and decoding. The API may
return fewer partial images than requested, including a final image without any
partials.

Color-block previews are intentionally low resolution and use a 256-color
palette. Their size and proportions use the terminal's reported cell dimensions
when available. If that metadata is missing or ambiguous, the renderer estimates
cells as twice as tall as wide; unusual font spacing can still affect the
approximation. Native Kitty/iTerm previews retain full image pixels and let the
terminal preserve their aspect ratio. None of these display choices resizes the
saved original.

Sharp font previews also fit their allocated character rows within the window.
The CLI checks again after preparing the font and recalculates any final-image
fallback after a resize. An older preview already in scrollback may still wrap
when the window becomes narrower; existing character mappings are not replaced.

Streaming supports one final image. Positive partial counts enable streaming
when saving unless `stream` is explicitly false or null, which is rejected.
Explicit API formats retain API events and require `--stream true`. Use
`--max-items -1` or omit it when saving so an event limit cannot hide the final
image. The 64 MiB/16 megapixel preview bounds apply only to rendering; they do
not limit final-image saving or API output.

After saving and printing the paths, interactive terminals can show the finished
images. The original saved bytes are unchanged. Saving to a pipe still prints
paths, without graphics, font changes or preview caches.
If another program changes a saved image before it is displayed, the CLI skips
that preview and warns you to check the output file.

```sh
openai images generate --prompt "A tiny orange robot" --inline off
openai images generate --prompt "A tiny orange robot" --inline on
```

Without a saved preference, `--inline auto` is the default on generation, edits
and variations. Kitty and
Ghostty use the Kitty graphics protocol; iTerm2 and WezTerm use the iTerm protocol.
Other color terminals show a labeled color-block approximation. `NO_COLOR` or
`CLICOLOR=0` disables that approximation, while native image graphics remain
available. Basic terminals keep the saved path without a preview.

`--inline on` explicitly allows the image-font path in a local Apple Terminal
tab. It registers a generated font containing image strips, preserving ordinary
text metrics, point size and profile settings. macOS may request Terminal
Automation permission. Without this opt-in, Apple Terminal uses color blocks.
Each tab has an immutable image gallery; a full gallery keeps earlier previews
and uses a fallback. Open another tab for more sharp previews. The setup and repair commands below
prepare or restore this same per-tab font.

Failed preparation removes only the new cache files it created. If font
registration may have succeeded, the CLI retains that attempt and falls back
for different images or font settings. Retry the same saved image and settings,
or open a new tab. Earlier previews and saved originals are retained.

Pipes, CI and `TERM=dumb` never render previews, even with `--inline on`.
Multiplexers use a color-block fallback when supported; they do not receive
native graphics or Apple font activation. Apple image fonts are unavailable over
SSH. Native Kitty rendering can work over SSH when the terminal identity is
forwarded. Explicit API formats and other saving opt-outs never render graphics.

Optional previews are limited to 64 MiB and 16 megapixels, and must fit the
terminal at a width of at least one cell. Larger or unusually tall images remain
saved in full; the CLI explains why they could not be displayed. Preview decoding
and font failures keep the saved files. Follow the printed path instead of
paying to generate the image again. No separate viewer is opened.

## Apple Terminal setup and repair

Apple Terminal's sharp previews use a font containing image pixels. Changing
the tab's text font can make earlier previews stop displaying correctly, even
though the saved image files are unchanged. Repair rebuilds the image font from
the retained preview cache using the selected text font and size.

Normal `openai images preview --inline on FILE` already prepares and repairs the font
as part of displaying an image. These optional commands let you do that work
without supplying a file or printing another image. Status only inspects the tab
and cache. None calls the API or generates an image:

```sh
openai images inline status          # read the current tab and cache
openai images inline setup           # prepare this tab, without a sample image
openai images inline repair          # restore its existing cached previews
openai images generate --prompt "A tiny orange robot" --inline on
```

Setup and repair require a local Apple Terminal tab on macOS, with output sent
directly to the terminal. SSH, terminal multiplexers and CI are unsupported.
Status reports that limitation on other hosts. Reading the exact Terminal tab
may request macOS Automation permission, including for status. Status does not
register fonts, change settings, write cache files or recover pending attempts;
it reports a snapshot, not a visual rendering test. It requires terminal output
to identify the Apple Terminal tab. These commands accept no positional arguments
and support readable `--format auto` or `--format text`; data formats,
`--transform` and `--raw-output` are rejected before native work.

Setup prepares an image-capable copy of the selected text face in advance;
it is not required before normal previewing. Repair uses the same transaction
but requires an existing cache. Both retain the selected text
face, point size, profile settings, original saved images, old preview fonts and
existing image character assignments. Neither adds sample glyphs nor changes
future automatic-preview preferences. Use `--inline on` or a saved `inline on`
preference for sharp Apple Terminal previews. Explicit `--inline auto` overrides
the saved preference and uses the color approximation.
Failed or canceled activation attempts conditional rollback without overwriting
concurrent user changes. Fonts whose registration may have succeeded are retained.

If the selected cached font file is missing, select your original text font and
size in Terminal, then run repair. If the image cache and allocation metadata
are intact, repair can rebuild from them. Missing metadata or thumbnails cannot
reconstruct earlier previews safely: retain the remaining cache and saved
originals, and use a new Terminal tab. A changed session identity cannot take
over another gallery's image font. Widen the window when status or repair reports
that existing image rows no longer fit.

If a new preview was interrupted after font registration, repair may refuse
because its image assignments are still pending. Retry the same saved image
with the same font settings, or use a new tab. Repair does not discard the pending
font or reconstruct which characters reached the terminal.

Repair identifies the cache using the current Terminal session ID and TTY.
It can restore lost font registration only while that identity and cache remain
available. It does not find or take over a previous tab's cache after a restart.

This adds explicit recovery only. The earlier combined prototype's sample/test
and destructive reset commands, gallery-capacity increases, cache-size redesign
and automatic scrollback reflow are not included. No reset or removal of committed previews is
performed by these commands. The existing transaction may clean up temporary
attempt files only when they are proven not to have reached native registration. Native Apple Terminal visual validation remains
separate from automated tests with a fake native bridge.
## Progress regression recording

The terminal-only tests use a synthetic loopback SSE server and a sized PTY.
An observer releases the final response only after both partial previews arrive.
Build and run them without API credentials:

```sh
go test -c -o /tmp/image-progress-custom.test ./pkg/custom
go test -c -o /tmp/image-progress-main.test ./cmd/openai
python3 scripts/check-image-progress.py /tmp/image-progress-custom.test /tmp/image-progress-main.test /tmp/image-progress-evidence
```

`scripts/demos/record-image-progress.sh` records a before/after comparison using
the shared fixture server and capture lifecycle. Its five arguments are the
before binary, after binary, their full commit IDs, and an empty output directory
outside the repository. Set `DEMO_API_BINARY` to a binary built from
`./scripts/demos`. Generated PNGs, GIFs and terminal captures stay outside Git.

## Saved images and preferences

```sh
openai images preview "path/to/image.png"
openai images preview --inline on "path/to/image.png"
openai images inline off
openai images inline on
openai images generate --prompt "A tiny orange robot" --inline auto
```

`images preview` displays an existing PNG, JPEG or WebP using the same renderer.
It needs no API key, makes no request, does not read stdin, and leaves the
original untouched. It works even when automatic previews are off. Explicit
preview defaults to `auto`; `--inline on` also permits Apple Terminal image-font
activation. Unsupported formats, output redirection, and images that cannot fit
the terminal return an error. `--format-error` and `--transform-error` still
control those errors. Local image commands accept `--format auto` or `text`,
but reject data formats, `--transform` and `--raw-output`.

Resize the window and run preview again to fit its current dimensions. A new
Apple Terminal preview width receives new glyph assignments; earlier mappings
and saved originals stay unchanged. Existing text rows can still wrap when the
window narrows. Each new width uses gallery capacity. A full gallery retains
earlier previews and falls back as described above; it never evicts them.

`images inline on|off` remembers the automatic preview setting across runs.
Turning it on also permits image-font activation in local Apple Terminal.
An explicit `--inline auto`, `on` or `off` overrides the saved setting for that
command. Turning previews off does not disable saving or explicit local preview.

The only setting file is `openai/image-preferences.json` inside the operating
system's user configuration directory: `~/Library/Application Support` on
macOS, `%AppData%` on Windows, and `$XDG_CONFIG_HOME` (or `~/.config`) on Linux.
Writes replace it atomically. Invalid, unknown or unreadable settings are kept;
automatic saving prints a warning and skips the preview. Use an explicit
`--inline` value to override them once, or move an invalid setting file aside
before saving a new preference. Other CLI settings and preview caches are not
rewritten. Existing global API configuration validation still runs for these
local commands, so stale base URL or mTLS settings may need correcting first.
