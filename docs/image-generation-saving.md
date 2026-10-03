# Image generation and saving

```sh
openai images generate --prompt "A tiny orange robot"
openai images generate --prompt "A tiny orange robot" --name robot --count 2
openai images generate --prompt "A tiny orange robot" --output-dir "~/Downloads"
openai images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare
openai images generate --prompt "A tiny orange robot" --output-format webp
openai --format json images generate --prompt "A tiny orange robot" --model gpt-image-2.5-sunburst
```

Run `openai images generate` without flags in a terminal to choose a prompt,
model, size, quality, background, file type and image count. The picker starts
with the CLI's default model, a 1024 × 1024 image, automatic quality and
background, PNG and one image. Enter generates from the prompt; use the arrow
keys or Tab to move through settings. Ctrl+P prints the command without
requesting an image, and Ctrl+C exits. Command previews and Ctrl+P support Bash,
zsh, fish and PowerShell 7. In other or unidentified shells, generation remains
available, but command printing is disabled with an explanation in the picker.

After a successful generation, the picker reopens below the saved result with
the same image settings and an empty prompt. It remembers the last submitted
image settings and save folder for next time; the description starts empty and
is not saved in picker preferences. Canceled edits are discarded. Settings stay
local to your user configuration folder and do not affect commands that supply flags.

Choose **Save to** to use the default folder, current directory, or another
existing folder. Tab completes folder names. Saved settings are optional: if
they cannot be written, a warning appears and generation can still continue.
Unknown or unreadable saved settings are left alone.
Explicit image flags, output formats, piped input and redirected output retain
the direct command behavior described below.

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

The `images create-variation` endpoint is retired and no longer available.
To create a variation, use `images edit` with a GPT Image model and a prompt:

```sh
openai images edit --image photo.png --prompt "Make the sky purple"
openai images edit --image first.png --image second.png --mask mask.png --prompt "Add a purple sky"
openai images edit --image photo.png --prompt "Make the sky purple" --name result --count 2
openai images edit --image photo.png --prompt "Make the sky purple" --stream true
openai images edit --image "photo.png" --prompt "Create a variation of this image" --name variation
openai --format json images edit --image photo.png --prompt "Make the sky purple"
```

Edits use the same saving folder, filename overrides, collision protection
and explicit-format opt-outs as generation. Source images
and masks are uploaded without being rewritten; saved results are new files.
Editing names come from the prompt.

Saving edits uses the generation preset above without `moderation`, which the
edit endpoint does not accept. Explicit models and nulls keep
the existing request semantics. In multipart requests, explicit null fields
retain their existing empty form-part encoding rather than acquiring defaults.

Streamed edits save only the final image. `--partial-images 1`, `2` or `3`
enables streaming and displays progress previews where supported.
Explicit `--format json` preserves the original responses or edit events and
makes no saved files. Model-discovery default markers remain separate work.
Use `openai help --all images edit` for every request setting.

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

Without a saved preference, `--inline auto` is the default on generation and
edits. Kitty and Ghostty use the Kitty graphics protocol; iTerm2 and WezTerm
use the iTerm protocol.
Other color terminals show a labeled color-block approximation. `NO_COLOR` or
`CLICOLOR=0` disables that approximation, while native image graphics remain
available. Basic terminals keep the saved path without a preview.

`--inline on` explicitly allows the image-font path in a local Apple Terminal
tab. It registers a generated font containing image strips, preserving ordinary
text metrics, point size and profile settings. macOS may request Terminal
Automation permission. Without this opt-in, Apple Terminal uses color blocks.
Each tab has an immutable image gallery; a full gallery keeps earlier previews
and uses a fallback. Open another tab for more sharp previews. Setup and repair
commands are separate work.

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
