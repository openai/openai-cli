package custom

const imageUploadQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{if eq .Name "edit"}}Edit an image
  {{$bin}} images edit --image "photo.png" --prompt "Make the sky purple"

Replace photo.png with your image's path and the prompt with your changes.
{{else}}Make an image variation
  {{$bin}} images create-variation --image "photo.png"

Replace photo.png with your image's path: a square PNG under 4 MB.
{{end}}Saves to ~/Downloads/gpt-images/. Keeps your original.

Full help: {{$bin}} help --all images {{.Name}}
`

const imageEditSavingHelp = `EXAMPLES

Edit and choose a filename:
    openai images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky
Remove a background:
    openai images edit --image "photo.png" --prompt "Remove the background" --background transparent --output-format webp

SAVING

Replace photo.png with your image's path. Keeps your original. Saves to ~/Downloads/gpt-images/ (created automatically). --output-dir chooses a folder that must already exist. Existing files are kept; duplicate names get -2, -3, etc.

DEFAULTS

With both --model and --response-format omitted: ` + defaultSavedImageModel + `, one PNG, automatic size, quality and background.
` + imageSavingOverrideHelp + `

PREVIEWS

` + imageProgressSettingsHelp + `
` + imagePreviewSettingsHelp + `

API OUTPUT

` + imageAPIOutputHelp

const imageVariationSavingHelp = `EXAMPLES

Make two variations:
    openai images create-variation --image "photo.png" --count 2 --size 512x512
Choose a filename:
    openai images create-variation --image "photo.png" --name variation

Variations support dall-e-2 only. photo.png must be an existing square PNG under 4 MB. Your key needs model access. No streaming or progress previews.

SAVING

Keeps your original. Saves to ~/Downloads/gpt-images/ (created automatically). --output-dir chooses a folder that must already exist. Existing files are kept; duplicate names get -2, -3, etc.
Saving uses dall-e-2 and b64_json when omitted. Explicit flags or JSON/YAML fields, including nulls, override these defaults.

PREVIEWS

` + imagePreviewSettingsHelp + `

API OUTPUT

` + imageAPIOutputHelp

const imageSavingOverrideHelp = `Explicit flags or JSON/YAML fields, including nulls, override these defaults. Setting model or response-format leaves other omitted settings to the API. DALL-E saving still requests b64_json when omitted.`

const imageProgressSettingsHelp = `- --partial-images 1 to 3 requests up to that many previews and enables streaming when saving. Only one final image is saved. Omit --max-items or use -1.
- --stream true alone requests no partial previews. For API events, use --format json --stream true.`

const imagePreviewSettingsHelp = `- --inline off hides previews once. openai images inline off remembers it; an explicit --inline overrides the preference. openai images preview FILE ignores it.
- Local Apple Terminal: auto uses color blocks; on (including a saved on preference) permits sharp previews, Terminal Automation and private preview caches. Pipes and CI never show previews.`

const imageAPIOutputHelp = `--format json returns API data: no saving or CLI image defaults. Other data formats, --transform, --raw-output and --response-format url also bypass saving; do not combine them with --name or --output-dir. --format auto and text save, even in pipes.`
