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
selects streaming when saving; intermediate images are ignored. An explicit
false or null stream conflicts with positive partials. Streaming supports one
final image, and `--max-items` cannot truncate a saving stream. For API events,
use an explicit data format and `--stream true`. An incomplete stream fails
without claiming that an image was saved. Only final saved images are previewed.

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

Streamed edits save only the final image and ignore intermediate previews.
Variations do not support streaming. Explicit `--format json` preserves the
original responses or edit events and makes no saved files. Model-discovery
default markers and redisplaying saved files remain separate work.
Use `openai help --all images edit` or `openai help --all images create-variation`
for every request setting.

## Inline previews

After saving and printing the paths, interactive terminals can show the finished
images. The original saved bytes are unchanged. Saving to a pipe still prints
paths, without graphics, font changes or preview caches.

```sh
openai images generate --prompt "A tiny orange robot" --inline off
openai images generate --prompt "A tiny orange robot" --inline on
```

`--inline auto` is the default on generation, edits and variations. Kitty and
Ghostty use the Kitty graphics protocol; iTerm2 and WezTerm use the iTerm protocol.
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
