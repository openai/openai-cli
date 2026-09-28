package custom

const imageUploadQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{if eq .Name "edit"}}Edit an image
  {{$bin}} images edit --image "photo.png" --prompt "Make the sky purple"

Replace photo.png with your image's path and the prompt with your changes.
{{else}}Make an image variation
  {{$bin}} images create-variation --image "photo.png"

Replace photo.png with your image's path: a square PNG under 4 MB.
{{end}}Saves to ~/Downloads/gpt-images/. Keeps your original.

All options: {{$bin}} help --all images {{.Name}}
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
automatically enables streaming and shows 1 to 3 progress previews where supported.
Only the final image is saved to the output folder. Apple Terminal's auto mode
uses color blocks. --inline on or a saved on preference enables sharp progress
and final previews; this may request Terminal Automation and keeps private
preview caches for scrollback. Unavailable sharp progress is skipped.
Streaming supports one final image. --format json --stream true returns API
events. Variations do not support streaming.

Interactive terminals show an inline preview when supported. --inline off disables
it. --inline on allows a local Apple Terminal image font. Pipes and CI never
show previews.`
