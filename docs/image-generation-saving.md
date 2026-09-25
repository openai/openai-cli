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
Existing filenames get suffixes such as `-2`. PNG, JPEG and WebP container
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
without claiming that an image was saved. No preview or viewer opens.

Editing, variations, model-discovery default markers, local previews and
terminal rendering are separate features. See `openai help --all images generate`
for the complete request settings.
