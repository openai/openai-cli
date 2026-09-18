package cmd

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
)

const imageOptionsPrefix = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}`

const imageOptionsOverview = `What would you like to change?
  {{$bin}} images options size

Replace size with a topic below:
  size          Square, portrait or landscape
  quality       Image detail
  count         Number of images (1-10)
  model         Exact model name
  format        PNG, JPEG or WebP
  background    Transparent or opaque
  partials      Progress previews
  save          Filename, folder or viewer
  upload        Edit an existing image
  moderation    Content filtering

Defaults are already set. Change only what you need.
More details: {{$bin}} images options --all
`

const imageOptionsDetails = `Image settings
  {{$bin}} images generate --prompt "A tiny orange robot"

That is all you need. In a terminal, the defaults are:
  Model: ` + defaultSavedImageModel + ` | Images: 1 | File: PNG
  Size, quality, background, moderation: auto | Partial images: none
  Saved automatically in ~/Downloads/gpt-images/.

CHANGE ONLY WHAT YOU WANT
  Add an option to the command above. It changes this command only.

  Model         --model gpt-image-2.5-flare   Use an exact model name
  Size          --size 1024x1536             Portrait (taller than wide)
  Quality       --quality high              Choose the detail level
  Image count   --count 2                    Make 1 to 10 images
  File format   --output-format webp         PNG, JPEG or WebP
  Background    --background transparent     Remove the background
  Moderation    --moderation low             Use less restrictive filtering
  Partial images --partial-images 2          See up to 2 previews as it generates

ALL CHOICES + AN EXAMPLE FOR ONE SETTING
  {{$bin}} images options size
  Replace size with: model, quality, count, format, background, moderation,
  partials, upload or save. Guides are free to read; generation uses API credits.

Exact model names: {{$bin}} images models
Complete API reference: {{$bin}} help --all images generate
`

type imageOptionTopic struct {
	name, usage, help string
}

// The first screen answers one question. The full guides below retain the
// compatibility details and script examples for callers who request --all.
var imageOptionBriefs = map[string]string{
	"model": `Choose a model
Default: ` + defaultSavedImageModel + ` (no --model needed).

  {{$bin}} images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare

Find exact model names: {{$bin}} images models
`,
	"size": `Choose an image shape

  {{$bin}} images generate --prompt "A tiny orange robot" --size 1024x1536

Choices: auto (default), 1024x1024 (square),
         1024x1536 (portrait), 1536x1024 (landscape).
`,
	"quality": `Choose image quality

  {{$bin}} images generate --prompt "A tiny orange robot" --quality high

Choices: auto (default), low, medium, high, xhigh (extra high), max.
Higher quality can take longer and cost more. Older models have fewer choices.
`,
	"count": `Choose how many images to make

  {{$bin}} images generate --prompt "A tiny orange robot" --count 2

Choose 1 to 10. Default: 1. Each image is saved separately.
More images cost more. Progress previews and DALL-E 3 allow only 1.
`,
	"format": `Choose the image file type

  {{$bin}} images generate --prompt "A tiny orange robot" --output-format webp

Choices: png (default), jpeg, webp.
Use PNG or WebP for transparency. The filename extension is automatic.
`,
	"background": `Choose the background

  {{$bin}} images generate --prompt "A robot sticker" --background transparent

Choices: auto (default), transparent (see-through), opaque (not see-through).
Transparency needs PNG (the default) or WebP.
`,
	"moderation": `Choose content filtering

  {{$bin}} images generate --prompt "A tiny orange robot" --moderation low

Choices: auto (default), low (less restrictive).
Low still applies safety checks.
`,
	"partials": `See progress while your image generates

  {{$bin}} images generate --prompt "A tiny orange robot" --partial-images 2

Choices: 0 (final only), 1, 2 or 3 progress previews. Default: 0.
Shows up to that many previews when enabled, then saves the final image.
One image at a time. Progress previews add API usage.
`,
	"upload": `Change an existing image
Replace ./robot.png with your image's path:

  {{$bin}} images edit --image ./robot.png --prompt "Make it blue" --model ` + defaultSavedImageModel + `

Accepts PNG, JPEG or WebP. Your original stays unchanged.
This command uses API credits and currently returns API data, not a saved file.
`,
	"save": `Choose where your image goes
Default: ~/Downloads/gpt-images/ (created automatically).

  {{$bin}} images generate --prompt "A tiny orange robot" --name robot

Also available: --output-dir "~/Downloads" to choose a folder,
                --open to open it, --inline off to hide the preview.
Names come from your prompt. Existing files are kept.
`,
}

var imageOptionTopics = []imageOptionTopic{
	{"model", "Choose an exact model name", `Choose a model
Default when saving: ` + defaultSavedImageModel + `.

Use the default (no --model needed):
  {{$bin}} images generate --prompt "A tiny orange robot"

Choose a different model by its exact name:
  {{$bin}} images generate --prompt "A tiny orange robot" --model gpt-image-2.5-flare

Find names and check visibility with your API key:
  {{$bin}} images models
  {{$bin}} images models --all
Show known names without an API call:
  {{$bin}} images models --offline

The list includes known public image models; it is not exhaustive.
You can pass another exact ID your account supports, including a dated version.
Seeing model information does not guarantee generation access or quota.
Other models can have different defaults, supported settings and limits.
`},
	{"size", "Choose auto, square, portrait or landscape", `Choose size and orientation
Default: auto. The model chooses dimensions for your description.

  --size auto        Automatic
  --size 1024x1024   Square
  --size 1024x1536   Portrait: taller than wide
  --size 1536x1024   Landscape: wider than tall

Make a portrait image:
  {{$bin}} images generate --prompt "A tiny orange robot" --size 1024x1536

Numbers are width x height, in pixels. Larger images can cost more.
Image 2 and 2.5 also accept other dimensions within their model limits.
The complete reference explains those limits and the sizes for older models:
  {{$bin}} help --all images generate
`},
	{"quality", "Choose auto, low, medium, high, xhigh or max", `Choose image quality
Default: auto. The model chooses the quality for your description.

  --quality auto     Automatic
  --quality low      Low
  --quality medium   Medium
  --quality high     High
  --quality xhigh    Extra high
  --quality max      Maximum

Make a high-quality image:
  {{$bin}} images generate --prompt "A tiny orange robot" --quality high

These choices work with the default Sunburst model. Extra high and max also
work with Image 2.5 Flare and their dated versions. Older models differ.
Higher quality can take longer and cost more. Auto is the easiest starting point.
`},
	{"count", "Generate 1 to 10 images", `Choose how many images to make
Default: 1. Choose any whole number from 1 to 10.

Make two images:
  {{$bin}} images generate --prompt "A tiny orange robot" --count 2

Make ten images:
  {{$bin}} images generate --prompt "A tiny orange robot" --count 10

Each image is saved separately. More images use more API credits.
--count 2, --n 2 and -n 2 mean the same thing.
Partial-image streaming supports one final image per request.
The older dall-e-3 model also supports only one image per request.
`},
	{"format", "Save PNG, JPEG or WebP files", `Choose the image file format
Default: png.

  --output-format png    PNG: preserves detail and supports transparency
  --output-format jpeg   JPEG: compressed image, no transparency
  --output-format webp   WebP: compression and transparency

Save a WebP:
  {{$bin}} images generate --prompt "A tiny orange robot" --output-format webp

JPEG and WebP also support --output-compression 0 through 100 (default: 100):
  {{$bin}} images generate --prompt "A tiny orange robot" --output-format jpeg --output-compression 80

The saved filename gets the correct extension automatically.
--format json is a separate option: it returns API data instead of saving files.
`},
	{"background", "Choose auto, transparent or opaque", `Choose the background
Default: auto.

  --background auto          Let the model choose
  --background transparent   Leave the background see-through
  --background opaque        Keep a visible, nontransparent background

Make an image with a transparent background:
  {{$bin}} images generate --prompt "A tiny orange robot sticker" --background transparent

Transparency requires PNG (the default) or WebP. JPEG cannot preserve it.
This works with Sunburst and Flare; support varies for older models.
`},
	{"moderation", "Choose auto or low content filtering", `Choose moderation
Default: auto. Uses standard content filtering.

  --moderation auto   Standard filtering
  --moderation low    Less restrictive filtering

Choose low moderation:
  {{$bin}} images generate --prompt "A tiny orange robot" --moderation low

Low does not turn off safety checks. This setting does not change image quality.
`},
	{"partials", "See previews while one image is being generated", `See an image while it is being generated
Default: none (--partial-images 0). You see the finished image.

  --partial-images 0   No intermediate previews
  --partial-images 1   Up to 1 preview
  --partial-images 2   Up to 2 previews
  --partial-images 3   Up to 3 previews

Request two intermediate previews:
  {{$bin}} images generate --prompt "A tiny orange robot" --partial-images 2

In a terminal, streaming starts automatically. The finished image is saved.
Previews appear when inline previews are enabled and supported. Preview files are
temporary; only the final image is kept in your output folder. There may be fewer previews
if the finished image is ready sooner. Partial images add API usage.
This works with one final image (--count 1), not a batch.
--inline off hides previews but does not remove their API usage.

For API events in a script, choose streaming and a model explicitly:
  {{$bin}} --format json images generate --prompt "A tiny orange robot" --model ` + defaultSavedImageModel + ` --stream true --partial-images 2
API-event output does not save images automatically.
`},
	{"upload", "Use an existing image as input", `Use an existing image (the attachment control)
Use images edit to change an existing image. The original file is kept.
Replace ./robot.png with the path to your image:

  {{$bin}} images edit --image ./robot.png --prompt "Make the robot blue" --model ` + defaultSavedImageModel + `

Add another --image PATH to supply another reference image.
For GPT Image models: PNG, JPEG or WebP, under 50 MB each, up to 16 images.
Editing makes an API request and uses credits.

Currently images edit returns API data; automatic saving and the friendly
preview workflow described in this guide apply to images generate.
For every editing option:
  {{$bin}} images edit --help

To view a file without editing it or using credits:
  {{$bin}} images preview ./robot.png
`},
	{"save", "Choose a filename, folder and preview behavior", `Save and view images
Images save automatically in ~/Downloads/gpt-images/ in a terminal.
The folder is created automatically. Existing files are kept.
"A tiny orange robot" is saved as tiny-orange-robot.png; --name overrides it.

Choose a name:
  {{$bin}} images generate --prompt "A tiny orange robot" --name robot
Save to another existing folder:
  {{$bin}} images generate --prompt "A tiny orange robot" --output-dir "~/Downloads"
Open the result in your image viewer:
  {{$bin}} images generate --prompt "A tiny orange robot" --open
Save without an inline preview:
  {{$bin}} images generate --prompt "A tiny orange robot" --inline off

Options work together:
  {{$bin}} images generate --prompt "A tiny orange robot" --name robot --count 2 --quality high

Piped or redirected output returns API data by default. Add --output-dir
or --name to save files from a script. --format json requests API data explicitly.
`},
}

func init() {
	// The framework's group-help hook ignores CustomHelpTemplate by default.
	// Honor it for our local guides without changing API resource-group help.
	showGroupHelp := cli.ShowSubcommandHelp
	cli.ShowSubcommandHelp = func(command *cli.Command) error {
		if local, _ := command.Metadata["local-help"].(bool); local {
			cli.HelpPrinter(command.Root().Writer, command.CustomHelpTemplate, command)
			return nil
		}
		return showGroupHelp(command)
	}
	for _, resource := range Command.Commands {
		if resource.Name != "images" {
			continue
		}
		guide := newImageOptionsCommand("options", "See image defaults, settings and examples", imageOptionsOverview, imageOptionsDetails)
		guide.Flags = []cli.Flag{&cli.BoolFlag{Name: "all", Usage: "Show the full explanation and examples", HideDefault: true}}
		for _, topic := range imageOptionTopics {
			brief := imageOptionBriefs[topic.name]
			if brief == "" {
				brief = topic.help
			}
			guide.Commands = append(guide.Commands, newImageOptionsCommand(topic.name, topic.usage,
				brief+"\nMore details: {{$bin}} images options "+topic.name+" --all\n",
				topic.help+"\nAll image settings: {{$bin}} images options\n"))
		}
		resource.Commands = append(resource.Commands, guide)
		return
	}
}

func newImageOptionsCommand(name, usage, brief, details string) *cli.Command {
	template := imageOptionsPrefix + `{{if .Bool "all"}}` + details + `{{else}}` + brief + `{{end}}`
	return &cli.Command{
		Name: name, Usage: usage, HideHelpCommand: true, Suggest: true,
		CustomHelpTemplate: template, Metadata: map[string]any{"local-help": true, "local-help-full": imageOptionsPrefix + details},
		Action: func(_ context.Context, command *cli.Command) error {
			if command.Args().Present() {
				return fmt.Errorf("Unknown settings topic %q. Run %s images options to see the choices.", command.Args().First(), imageHelpInvocation(command))
			}
			cli.HelpPrinter(command.Root().Writer, template, command)
			return nil
		},
	}
}
