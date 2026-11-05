package images

const imageUploadQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{if eq .Name "edit"}}Edit an image
  {{suggest . (printf "%s images edit --image \"photo.png\" --prompt \"Make the sky purple\"" $bin)}}

Replace photo.png with your image's path and the prompt with your changes.
Saves to ~/Downloads/gpt-images/. Keeps your original.

COMMON OPTIONS
  --model MODEL          Choose a model ID
  --size SIZE            e.g. 1024x1024
  --quality QUALITY      auto, low, medium, high
  --name NAME            Name the saved image
  --output-dir DIRECTORY Save in an existing folder
Size and quality choices depend on the model.
{{else}}Image variations are retired and no longer available.
Use images edit with a GPT Image model and a prompt:
  {{suggest . (printf "%s images edit --image \"photo.png\" --prompt \"Create a variation of this image\"" $bin)}}

Replace photo.png with your image's path. Edits save to ~/Downloads/gpt-images/.
{{end}}
Help: {{suggest . (printf "%s help images %s" $bin .Name)}}
`

const imageEditExample = `images edit --image "photo.png" --prompt "Make the sky purple" --name purple-sky`
const imageVariationExample = `images edit --image "photo.png" --prompt "Create a variation of this image" --name variation`

const imageEditSavingHelp = `EXAMPLE

    openai ` + imageEditExample + `

Keeps your original. Saves to ~/Downloads/gpt-images/.
Use --output-dir to choose an existing folder.
For API data without saving, use --format json without --name or --output-dir.
`

const imageVariationSavingHelp = `This endpoint is retired and no longer available. Use images edit with a GPT Image model and a prompt:

    openai ` + imageVariationExample + `

The options below describe the legacy variations contract.
`
