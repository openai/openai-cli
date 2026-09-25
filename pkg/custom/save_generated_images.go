package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/imageoutput"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// This preset is an exact catalog ID, not a claim about account permissions or
// latest availability. Explicit API output retains the API's own defaults.
const defaultSavedImageModel = "gpt-image-2.5-sunburst"

const imageGenerationQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Make an image
  {{$bin}} images generate --prompt "A tiny orange robot"

Default: 1 PNG, automatic size and quality.
Model: ` + defaultSavedImageModel + `. Model access varies by key.
Saves to ~/Downloads/gpt-images/ and creates the folder automatically.
Names come from your prompt. Existing files are kept. Every saved path is printed.

Optional:
  --name robot                 Choose a filename (extension is automatic)
  --output-dir "~/Downloads"    Save in an existing folder
  --count 2                    Make two images (alias for -n)
  --model gpt-image-2.5-flare    Choose an exact model ID
  --output-format webp         Choose PNG, JPEG or WebP

Scripts: --format json returns API JSON without saving or CLI defaults.
Redirected output still saves images. No image preview is opened.
Full help: {{$bin}} help --all images generate
Key setup: {{$bin}} help setup
`

const imageGenerationSavingHelp = `Images save automatically to ~/Downloads/gpt-images/, including in pipes.
The default folder is created. --output-dir chooses an existing folder.
Names come from the prompt; --name overrides them. Existing names get -2, -3,
etc. The returned PNG, JPEG or WebP bytes determine the file extension.

When neither model nor response-format is supplied, saving uses
` + defaultSavedImageModel + `, one PNG, automatic size, quality, background
and moderation, no partial images, and no streaming. Flags and JSON/YAML stdin
override these defaults, including nulls. --count is an alias for -n.
Explicit models keep API defaults; explicit DALL-E models request b64_json.
Your account must support the chosen model.

--format json (or another data format), --transform, --raw-output and
--response-format url keep API output without saving or applying CLI defaults.
They cannot be combined with --name or --output-dir. --format auto and text save.
--output-format selects the image file format, separately from --format.

--stream true saves only the final image. Positive --partial-images enables
streaming when saving; intermediate images are ignored. Streaming supports one
final image. Use --format json --stream true for complete API events.
No image preview or viewer is opened.`

type imageOutputPlan struct {
	directory, name string
	options         []option.RequestOption
}

// A private classification carries locally authored guidance through the shared
// error presenter. Causes remain available to errors.Is without printing data.
type imageSavingError struct {
	message string
	cause   error
}

func (e *imageSavingError) Error() string                  { return e.message }
func (e *imageSavingError) Unwrap() error                  { return e.cause }
func imageSavingFailure(message string, cause error) error { return &imageSavingError{message, cause} }

func prepareImageGeneration(ctx context.Context, command *cli.Command, body gjson.Result) (*imageOutputPlan, bool, error) {
	streaming := body.Get("stream").Type == gjson.True
	if err := validateImageGenerationSettings(body); err != nil {
		return nil, false, imageSavingFailure(err.Error(), err)
	}
	var conflict string
	format := strings.ToLower(command.Root().String("format"))
	switch {
	case format != "" && format != "auto" && format != "text":
		conflict = "an API output format"
	case command.Root().String("transform") != "" || command.Root().Bool("raw-output"):
		conflict = "--transform or --raw-output"
	case body.Get("response_format").String() == "url":
		conflict = "--response-format url"
	}
	if conflict != "" {
		if command.IsSet("name") || command.IsSet("output-dir") {
			return nil, false, imageSavingFailure("--name and --output-dir cannot be combined with "+conflict+"; choose saved images or API output.", nil)
		}
		if body.Get("response_format").String() != "url" && body.Get("partial_images").Int() > 0 && !streaming {
			return nil, false, imageSavingFailure("--partial-images needs --stream true for API output.", nil)
		}
		return nil, streaming, nil
	}
	partials := body.Get("partial_images").Int()
	if partials > 0 {
		if body.Get("stream").Exists() && !streaming {
			return nil, false, imageSavingFailure("--partial-images needs streaming; omit --stream or use --stream true.", nil)
		}
		streaming = true
	}
	if streaming && command.IsSet("max-items") {
		return nil, false, imageSavingFailure("--max-items could stop before the final image; omit it when saving or use --format json for API events.", nil)
	}
	name := command.String("name")
	var stem string
	if command.IsSet("name") {
		var err error
		stem, err = imageoutput.NormalizeName(name)
		if err != nil {
			return nil, false, imageSavingFailure("--name: "+err.Error(), err)
		}
	} else if body.Get("prompt").Type == gjson.String {
		name = imageoutput.NameFromPrompt(body.Get("prompt").String())
		stem = name
	}
	if command.IsSet("output-dir") && command.String("output-dir") == "" {
		return nil, false, imageSavingFailure("--output-dir must name an existing writable directory.", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	directory, err := imageoutput.ResolveDirectory(command.String("output-dir"))
	if err != nil {
		return nil, false, imageSavingFailure("Could not prepare the image output folder. Choose an existing writable directory with --output-dir, or omit it to create ~/Downloads/gpt-images/.", err)
	}
	if err := imageoutput.CheckName(ctx, directory, stem); err != nil {
		return nil, false, imageSavingFailure("Could not use this image filename. Try a shorter --name or another --output-dir.", err)
	}
	plan := &imageOutputPlan{directory: directory, name: name}
	set := func(field string, value any) { plan.options = append(plan.options, option.WithJSONSet(field, value)) }
	model := body.Get("model")
	if !model.Exists() && !body.Get("response_format").Exists() {
		set("model", defaultSavedImageModel)
		for _, preset := range []struct {
			field string
			value any
		}{
			{"n", 1}, {"size", "auto"}, {"quality", "auto"}, {"output_format", "png"},
			{"background", "auto"}, {"moderation", "auto"}, {"partial_images", 0}, {"stream", false},
		} {
			if !body.Get(preset.field).Exists() {
				set(preset.field, preset.value)
			}
		}
	} else if (model.String() == "dall-e-2" || model.String() == "dall-e-3") && !body.Get("response_format").Exists() {
		set("response_format", "b64_json")
	}
	if streaming && !body.Get("stream").Exists() {
		set("stream", true)
	}
	return plan, streaming, nil
}

func (p *imageOutputPlan) save(ctx context.Context, response []byte, out io.Writer) error {
	paths, saveErr := imageoutput.SaveResponse(ctx, response, p.directory, p.name)
	out = outputWriter{ctx: context.WithoutCancel(ctx), out: out}
	if saveErr != nil {
		message := "The API responded, but no images could be saved. Check the image data and output folder before trying again."
		if len(paths) > 0 {
			message = fmt.Sprintf("Saved %d image(s), but could not save the entire response. The listed files are kept; do not generate those images again.", len(paths))
		}
		saveErr = imageSavingFailure(message, saveErr)
	}
	for _, path := range paths {
		// Quoting keeps paths useful without allowing terminal control injection.
		// Still report completed paths if cancellation occurred on a later image.
		if _, err := fmt.Fprintf(out, "Saved image: %q\n", path); err != nil {
			return errors.Join(saveErr, imageSavingFailure("Images were saved, but their paths could not be printed. Check the output folder before generating again.", err))
		}
	}
	return saveErr
}
