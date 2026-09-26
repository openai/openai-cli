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
without claiming that an image was saved. No preview or viewer opens.

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
default markers, local previews and terminal rendering remain separate work.
Use `openai help --all images edit` or `openai help --all images create-variation`
for every request setting.
