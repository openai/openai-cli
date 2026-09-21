package custom

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// Keep the first help screen focused on making an image. The full reference
// renders the real flag definitions, including parameters added by generation.
const imageGenerateQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Make an image
  {{$bin}} images generate --prompt "A tiny orange robot"

In a terminal: 1 PNG, automatic size and quality.
Model: ` + defaultSavedImageModel + `.
Saves to ~/Downloads/gpt-images/ and creates the folder automatically.
Shows a preview when enabled and supported. Existing images are kept.

Optional: add one of these to the command above.
  --name robot                    Save as robot.png (existing files kept)
  --output-dir "~/Downloads"       Save to an existing folder
  --model gpt-image-2.5-flare       Use another image model
  --count 2                       Make 2 images (choose 1 to 10)
  --open                          Open it in a separate window
  --inline off                    Save it without showing a preview

Settings explained: {{$bin}} images options | Models: {{$bin}} images models
Complete API reference: {{$bin}} help --all images generate
API key setup: {{$bin}} help setup
Scripts: --format json or redirected output returns API data by default.
`

const imageGenerateFullHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Image generation: full reference
  {{$bin}} images generate --prompt TEXT [options]

{{.Description}}

IMAGE OPTIONS
{{range .VisibleFlags}}{{call (index $.Metadata "image-reference-flag") .}}{{end}}
GLOBAL OPTIONS
{{range .VisiblePersistentFlags}}{{call (index $.Metadata "image-reference-flag") .}}{{end}}`

// A display-only adapter prevents the framework from treating Markdown code
// spans in API prose as argument placeholders (for example --prompt dall-e-2).
// Parsing, required flags, defaults, aliases and generated Usage stay untouched.
type imageReferenceFlag struct {
	cli.Flag
	cli.DocGenerationFlag
}

func (f imageReferenceFlag) GetUsage() string {
	return strings.TrimSpace(strings.ReplaceAll(f.DocGenerationFlag.GetUsage(), "`", ""))
}

func (f imageReferenceFlag) GetValue() string {
	value := f.DocGenerationFlag.GetValue()
	// Root help never parses the target command. Read request flags' declared
	// default without initializing them or running their validators/sources.
	if getter, ok := f.Flag.(interface{ Get() any }); ok && value == "" {
		v := reflect.ValueOf(getter.Get())
		if v.IsValid() && v.Kind() == reflect.Pointer && !v.IsNil() {
			value = fmt.Sprint(v.Elem().Interface())
		}
	}
	return value
}

func (f imageReferenceFlag) IsRequired() bool {
	flag, ok := f.Flag.(cli.RequiredFlag)
	return ok && flag.IsRequired()
}

func (f imageReferenceFlag) IsMultiValueFlag() bool {
	flag, ok := f.Flag.(cli.DocGenerationMultiValueFlag)
	return ok && flag.IsMultiValueFlag()
}

func renderImageReferenceFlag(flag cli.Flag) string {
	formatted := flag.String()
	if doc, ok := flag.(cli.DocGenerationFlag); ok {
		formatted = cli.FlagStringer(imageReferenceFlag{flag, doc})
	}
	name, description, _ := strings.Cut(formatted, "\t")
	var out bytes.Buffer
	cli.HelpPrinterCustom(&out, "  {{.Name}}\n    {{wrap .Description 4}}\n\n", struct{ Name, Description string }{name, description}, map[string]any{
		"wrapAt": func() int { return 88 },
	})
	return out.String()
}

// @CLI@ is substituted as plain text, not interpreted as a template or shell
// expression. Keep examples usable for local ./openai builds as well as installs.
const imageGenerateDetails = `DEFAULTS WHEN SAVING
  Model: ` + defaultSavedImageModel + `. Images: 1. Size: auto. Quality: auto. Format: png.
  These apply when neither --model nor legacy --response-format is supplied.
  Your flags and JSON/YAML stdin values override the preset, including nulls.
  Explicit models and API output use API defaults for omitted settings.
  Background: auto. Moderation: auto. Partial images: 0 (none). Streaming: false.

SETTINGS EXPLAINED
  @CLI@ images options shows everyday choices and links to short guides.
  For example: @CLI@ images options quality
  --count is a readable alias for -n; both choose 1 to 10 images.

PROGRESS PREVIEWS
  Add --partial-images 1, 2 or 3 when saving to stream progress previews.
  Streaming starts automatically. Only the final image is saved in your folder.
  There may be fewer previews if the final image is ready sooner.
  Streaming supports one final image per request; use --count 1.
  Previews add API usage; --inline off hides them but does not stop that usage.

CHOOSE A MODEL
  Use the exact model ID, for example --model gpt-image-2.5-flare.
  Find known image models and check visibility: @CLI@ images models
  Show names without an API call: @CLI@ images models --offline
  Include dated versions and retired/not-visible models: @CLI@ images models --all
  The check retrieves model information; generation permissions can differ.

SAVE AND NAME
  In a terminal, images save to ~/Downloads/gpt-images/ automatically.
  That folder is created automatically. --output-dir chooses an existing folder.
  Filenames come from your prompt: "A tiny orange robot" becomes tiny-orange-robot.png.
  --name robot overrides the automatic name (for PNG output: robot.png).
  Long prompts use a short word-based name; prompts without usable text use the date/time.
  Names already in use get -2, -3, etc. Existing files are never overwritten.
  --name takes a name without a path. A final .png, .jpg, .jpeg or .webp is optional;
  the returned image's actual format chooses the extension. Every saved path is printed.

VIEW YOUR IMAGE
  Previews start on. --inline on or off overrides your preference for one run.
  Remember a preference: @CLI@ images inline off (or on).
  --open opens the saved original in your desktop viewer instead of inline.
  Add --inline on with --open to use both. A desktop viewer must be available.
  iTerm2, Ghostty and Kitty support sharp inline images. For Apple Terminal,
  run @CLI@ images inline setup (experimental). Other terminals use text previews.
  View a saved file without an API call: @CLI@ images preview FILE
  Open it in a separate window: @CLI@ images preview --open FILE
  Replace FILE with the saved path. Viewing an existing file uses no API credits.

SCRIPTS AND API OUTPUT
  Piped or redirected output returns API data by default, without saving images.
  Use --output-dir or --name to save in scripts. Previews require terminal output.
  --format json returns API data without saving; it does not choose the image format.
  --output-format png, jpeg or webp chooses the actual image file format.
  --response-format is a separate, legacy DALL-E API setting (url or b64_json).
  Saving flags (--output-dir, --name, --open) cannot be combined with an explicit
  data format, --transform, --raw-output or --response-format url.
  --stream true by itself emits API events. Add a saving flag to save its final image.
  For partial previews in a terminal, just use --partial-images (see above).
  Partial images in API-output mode require --stream true and an explicit model.

WHEN SOMETHING GOES WRONG
  Interactive saving shows a short explanation and a next step.
  Add --format-error json for the full API error. Redirected output, explicit
  data/error formats and --debug keep the usual API error details.

EXAMPLES
  @CLI@ images generate --prompt "A tiny orange robot"
  @CLI@ images generate --prompt "A tiny orange robot" --name robot --open
  @CLI@ images generate --prompt "A tiny orange robot" --inline off
  @CLI@ images generate --prompt "A tiny orange robot" --output-dir "$HOME/Pictures"
  @CLI@ --format json images generate --model ` + defaultSavedImageModel + ` --prompt "A tiny orange robot"

OPTIONS BELOW
  Includes the complete generated API descriptions and model-specific limits.
  API defaults below apply to API output; the CLI saving preset is described above.
  Model guide: https://developers.openai.com/api/docs/guides/image-generation`

func configureImageHelp(root *cli.Command, invocation string) {
	images := root.Command("images")
	if images == nil {
		return
	}
	if imagesGenerate := images.Command("generate"); imagesGenerate != nil {
		imagesGenerate.UsageText = invocation + " images generate --prompt TEXT [options]"
		imagesGenerate.Description = strings.ReplaceAll(imageGenerateDetails, "@CLI@", invocation)
	}
	if preview := images.Command("preview"); preview != nil {
		preview.UsageText = invocation + " images preview [--open] FILE"
		preview.Description = strings.ReplaceAll(imagePreviewDetails, "@CLI@", invocation)
	}
}

func imageHelpInvocation(command *cli.Command) string {
	invocation, _ := command.Root().Metadata["help-invocation"].(string)
	if invocation == "" {
		return "openai"
	}
	return invocation
}

// Image descriptions are flag values, not positional arguments. Explain that
// common first-run mistake without echoing a potentially private description.
func imageGenerateAction(action cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		if command.Args().Len() > 0 {
			invocation := imageHelpInvocation(command)
			return fmt.Errorf("Unexpected extra arguments. Put your description after --prompt and inside quotes.\nTry: %s images generate --prompt \"A tiny orange robot\"\nHelp: %s images generate --help", invocation, invocation)
		}
		return action(ctx, command)
	}
}

// The framework's default usage-error path ignores CustomHelpTemplate and dumps
// every generated flag. Keep mistakes actionable without losing the original
// error or exit status. Quoting also makes unexpected flag text safe to display.
func imageGenerateUsageError(_ context.Context, command *cli.Command, err error, _ bool) error {
	invocation := imageHelpInvocation(command)
	out := command.Root().ErrWriter
	if provided, ok := strings.CutPrefix(err.Error(), "flag provided but not defined: -"); ok {
		provided = strings.TrimLeft(provided, "-")
		prefix := "--"
		if len(provided) == 1 {
			prefix = "-"
		}
		fmt.Fprintf(out, "Unknown option %q.\n", prefix+provided)
		flags := append(command.VisibleFlags(), command.VisiblePersistentFlags()...)
		if suggestion := cli.SuggestFlag(flags, provided, command.HideHelp); suggestion != "" {
			fmt.Fprintf(out, "Did you mean %q?\n", suggestion)
		}
	} else if provided, ok := strings.CutPrefix(err.Error(), "flag needs an argument: -"); ok {
		provided = strings.TrimLeft(provided, "-")
		prefix := "--"
		if len(provided) == 1 {
			prefix = "-"
		}
		fmt.Fprintf(out, "Option %q needs a value.\n", prefix+provided)
		if provided == "prompt" {
			fmt.Fprintf(out, "Try: %s images generate --prompt \"A tiny orange robot\"\n", invocation)
		}
	} else {
		fmt.Fprintf(out, "Could not read the command options: %q\n", err.Error())
	}
	fmt.Fprintf(out, "Help: %s images generate --help\n", invocation)
	return err
}

// Presentation belongs beside the handwritten saving policy. Keep the generated
// API flags intact so new parameters retain their generated help automatically.
func registerImageGenerate(imagesGenerate *cli.Command) {
	imagesGenerate.Usage = "Generate images from a text prompt."
	imagesGenerate.UsageText = "openai images generate --prompt TEXT [options]"
	imagesGenerate.CustomHelpTemplate = imageGenerateQuickHelp
	imagesGenerate.OnUsageError = imageGenerateUsageError
	imagesGenerate.Action = imageGenerateAction(imageGenerateWorkflow(imagesGenerate.Action))
	if imagesGenerate.Metadata == nil {
		imagesGenerate.Metadata = map[string]any{}
	}
	imagesGenerate.Metadata["image-reference-flag"] = renderImageReferenceFlag
	imagesGenerate.Metadata["image-generate"] = true
	imagesGenerate.Description = strings.ReplaceAll(imageGenerateDetails, "@CLI@", "openai")
	imagesGenerate.Flags = append(imagesGenerate.Flags, &cli.StringFlag{
		Name:        "output-dir",
		Usage:       "Save images to an existing `DIRECTORY` (also works in scripts)",
		DefaultText: "~/Downloads/gpt-images/ in a terminal",
	}, &cli.StringFlag{
		Name:        "inline",
		Usage:       "Inline preview `MODE`: on or off (overrides your saved preference)",
		Value:       "on",
		DefaultText: "saved preference, initially on",
	}, &cli.StringFlag{
		Name:  "name",
		Usage: "Save as `NAME` (for example, robot or robot.png); the image format supplies the extension",
	}, &cli.BoolFlag{
		Name: "open", Usage: "Save and open the original in your default image viewer", HideDefault: true,
	}, &cli.BoolFlag{
		Name:        "no-preview",
		Usage:       "Save images without displaying terminal previews",
		HideDefault: true,
		Hidden:      true, // Keep the earlier opt-out working; prefer --inline off.
	})
	for _, flag := range imagesGenerate.Flags {
		switch flag := flag.(type) {
		case *requestflag.Flag[string]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*string]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[int64]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*int64]:
			setImageFlagHelp(flag)
		case *requestflag.Flag[*bool]:
			setImageFlagHelp(flag)
		}
	}
	// Put the everyday options first; keep all other and future flags visible.
	order := map[string]int{"prompt": 1, "model": 2, "open": 3, "inline": 4, "name": 5, "output-dir": 6, "size": 7, "n": 8, "quality": 9, "output-format": 10}
	rank := func(flag cli.Flag) int {
		if n := order[flag.Names()[0]]; n != 0 {
			return n
		}
		return len(order) + 1
	}
	sort.SliceStable(imagesGenerate.Flags, func(i, j int) bool {
		return rank(imagesGenerate.Flags[i]) < rank(imagesGenerate.Flags[j])
	})
}

func setImageFlagHelp[T any](flag *requestflag.Flag[T]) {
	// Full help must retain the original API contract, including future model
	// limits. Add CLI context rather than replacing the generated explanation.
	switch flag.Name {
	case "prompt":
		flag.Usage = "Required: supply --prompt TEXT or prompt in JSON/YAML stdin. " + flag.Usage
	case "model":
		flag.Usage = "CLI saving preset: " + defaultSavedImageModel + " (see DEFAULTS WHEN SAVING). API behavior: " + flag.Usage
		flag.HideDefault = true
	case "n":
		flag.Aliases = append(flag.Aliases, "count")
		flag.Usage = "Use --count NUMBER (or -n NUMBER) to choose the number of images. " + flag.Usage
	case "partial-images":
		flag.Usage = "When saving, 1 to 3 automatically enables streaming previews; 0 means none. API behavior: " + flag.Usage
	case "response-format":
		flag.Usage = "For saving with an explicit DALL-E model, the CLI requests b64_json when this is omitted. API behavior: " + flag.Usage
		flag.HideDefault = true
	case "size":
		// A nil request value means omitted, not a model-independent null default.
		flag.HideDefault = true
	case "max-items":
		flag.Usage += " Counts emitted streaming events, not generated images; it is not a generation or cost limit."
		flag.DefaultText = "unlimited"
	}
}
