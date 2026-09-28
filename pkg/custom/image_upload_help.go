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

const imageEditSavingHelp = `SAVE FOLDER AND NAME

Edited images save to ~/Downloads/gpt-images/, created automatically. Source files and existing outputs are kept. Names come from the prompt, with a timestamp fallback; --name chooses a filename stem. Collisions add -2, -3, etc. Returned bytes determine the extension.
    openai images edit --image "photo.png" --prompt "Make the sky purple" --output-dir "~/Downloads" --name purple-sky
photo.png is your existing image. The chosen output folder must already exist. The CLI expands the quoted ~ to your home folder on every platform.

SAVING DEFAULTS

With both --model and --response-format omitted: ` + defaultSavedImageModel + `, one PNG, automatic size, quality and background; no partial images or streaming.
` + imageSavingOverrideHelp + `

IMAGE SETTINGS

--count (alias -n) chooses 1 to 10 images. --size and --quality depend on the model; see the option reference for limits. --model accepts an exact ID your key can access. Repeat --image for multiple source files; --mask selects the area to edit.
    openai images edit --image "photo.png" --prompt "Make the sky purple" --size 1024x1536 --quality low
GPT Image edits support png, jpeg and webp through --output-format. Transparent backgrounds need png or webp. --output-compression accepts 0 to 100 for jpeg/webp only; the API default is 100. There is no --moderation flag for edits.
    openai images edit --image "photo.png" --prompt "Remove the background" --background transparent --output-format webp --output-compression 80

PROGRESS PREVIEWS
    openai images edit --image "photo.png" --prompt "Make the sky purple" --partial-images 2
` + imageProgressSettingsHelp + `

TERMINAL PREVIEWS
    openai images edit --image "photo.png" --prompt "Make the sky purple" --inline off
` + imagePreviewSettingsHelp + `

API OUTPUT
    openai --format json images edit --image "photo.png" --prompt "Make the sky purple" --model gpt-image-2.5-sunburst
` + imageAPIOutputHelp

const imageVariationSavingHelp = `SOURCE IMAGE

Variations support dall-e-2 only. Use an existing square PNG under 4 MB; photo.png below means that file's path. Your key must have model access.

SAVE FOLDER AND NAME

New images save to ~/Downloads/gpt-images/, created automatically. Source files and existing outputs are kept. The default name is image-variation; --name overrides it. Collisions add -2, -3, etc. Returned bytes determine the extension.
    openai images create-variation --image "photo.png" --output-dir "~/Downloads" --name variation
The chosen folder must already exist. The CLI expands the quoted ~ to your home folder on every platform.

MODEL, COUNT AND SIZE

Saving defaults to dall-e-2 and requests b64_json when --response-format is omitted. Other omitted settings use API defaults. Flags and JSON/YAML input override these defaults, including explicit nulls.
--count (alias -n) chooses 1 to 10 images. --size supports 256x256, 512x512 and 1024x1024.
    openai images create-variation --image "photo.png" --count 2 --size 512x512
Variations do not support streaming, progress previews, transparency or file-format selection.

TERMINAL PREVIEWS
    openai images create-variation --image "photo.png" --inline off
` + imagePreviewSettingsHelp + `

API OUTPUT
    openai --format json images create-variation --image "photo.png"
` + imageAPIOutputHelp

const imageSavingOverrideHelp = `Flags and JSON/YAML stdin override these saving defaults, including explicit nulls. An explicit model or response-format keeps API defaults for other omitted settings. DALL-E saving requests b64_json when response-format is omitted.
The option reference below documents API/model behavior. A displayed flag default is not necessarily sent by the CLI.`

const imageProgressSettingsHelp = `--partial-images 1 to 3 requests up to that many previews, not a guaranteed count. Positive values enable streaming when saving. --stream true by itself streams without requesting partial previews. Streaming supports one final image; omit --max-items or use -1.
Only the final image is saved to the output folder. Apple Terminal's auto mode uses color blocks. --inline on or a saved on preference enables sharp progress and final previews; this may request Terminal Automation and keeps private preview caches for scrollback. Unavailable sharp progress is skipped.
Use --format json --stream true for API events; in API-output mode, positive --partial-images values require --stream true.`

const imagePreviewSettingsHelp = `Preview support depends on the terminal. --inline auto uses native graphics or a color approximation; --inline on also permits sharp previews in local Apple Terminal; --inline off hides previews for this command. Pipes and CI never show previews. Preview failures keep saved files.
Remember a preference for future generation, edits and variations:
    openai images inline off
    openai images inline on
Turning on permits Apple Terminal font activation. An explicit --inline auto, on or off overrides the saved preference for one command. View a saved image without another API request using openai images preview "photo.png"; that command ignores the preference.`

const imageAPIOutputHelp = `--format json chooses the API response instead: no saving or CLI image defaults. Other explicit data formats, --transform, --raw-output and --response-format url also bypass saving. They cannot be combined with --name or --output-dir. --format auto and text save even when stdout is redirected. URL output is never downloaded.`
