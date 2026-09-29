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

const imageEditSavingHelp = `EXAMPLE

    openai images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky

Keeps your original. Saves to ~/Downloads/gpt-images/.
Use --output-dir to choose an existing folder.
For API data without saving, use --format json without --name or --output-dir.
`

const imageVariationSavingHelp = `EXAMPLE

    openai images create-variation --image "photo.png" --name variation

Requires a square PNG under 4 MB. Supports dall-e-2 only.
Keeps your original. Saves to ~/Downloads/gpt-images/.
Use --output-dir to choose an existing folder.
For API data without saving, use --format json without --name or --output-dir.
`
