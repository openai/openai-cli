package custom

const imageUploadQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{if eq .Name "edit"}}Edit an image
  {{$bin}} images edit --image "photo.png" --prompt "Make the sky purple"

Default model: ` + defaultSavedImageModel + `.
Repeat --image for multiple source files. --mask selects a mask file.
{{else}}Make an image variation
  {{$bin}} images create-variation --image "photo.png"

Uses dall-e-2. Requires a square PNG under 4 MB.
{{end}}Saves new images to ~/Downloads/gpt-images/. Source files are kept.
Model access varies by key. No preview or viewer is opened.

Optional:
  --name result                 Choose a filename (extension is automatic)
  --output-dir "~/Downloads"    Save in an existing folder
  --count 2                    Make two images (alias for -n)

Scripts: --format json returns API data without saving or CLI defaults.
Full help: {{$bin}} help --all images {{.Name}}
`

const imageUploadSavingHelp = `Edited images and variations save automatically to
~/Downloads/gpt-images/, including in pipes. Source files and existing outputs
are kept. --output-dir chooses an existing folder; --name supplies a filename.
Edits use prompt-based names; variations use image-variation. Collisions add
-2, -3, etc. Returned bytes determine PNG, JPEG or WebP extensions.

When model and response-format are omitted, edits use ` + defaultSavedImageModel + `,
one PNG, automatic size, quality and background, no partial images or streaming.
Variations default to dall-e-2 with b64_json. Flags and JSON/YAML input override
saving defaults, including explicit nulls. --count is an alias for -n.
Explicit models keep API defaults; DALL-E saving requests b64_json when omitted.

Explicit data formats, --transform, --raw-output and --response-format url
return API data without saving or CLI defaults. They cannot be combined with
--name or --output-dir. --format auto and text save. No URL is downloaded.

For edits, --stream true saves only the final image. Positive --partial-images
automatically enables streaming; intermediate images are ignored, not saved.
Streaming supports one final image. --format json --stream true returns API
events. Variations do not support streaming. No preview or viewer is opened.`
