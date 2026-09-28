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

Replace the prompt with the image you want.
Saves to ~/Downloads/gpt-images/.

Full help: {{$bin}} help --all images generate
Key setup: {{$bin}} help setup
`

const imageGenerationSavingHelp = `SAVE FOLDER AND NAME

Images save automatically to ~/Downloads/gpt-images/, including when stdout is redirected. The default folder is created; a chosen folder must already exist.
    openai images generate --prompt "A tiny cat" --output-dir "~/Downloads" --name cat
The CLI expands the quoted ~ to your home folder on every platform. Names normally come from the prompt, with a timestamp fallback. --name chooses a filename stem. Name collisions add -2, -3, etc.; existing files are kept. Returned bytes determine the extension.

SAVING DEFAULTS

With both --model and --response-format omitted: ` + defaultSavedImageModel + `, one PNG, automatic size, quality, background and moderation; no partial images or streaming.
` + imageSavingOverrideHelp + `

COUNT, SIZE AND QUALITY

--count is an alias for -n. Size, quality and count limits depend on the model; see the option reference below.
    openai images generate --prompt "A tiny orange robot" --count 2
    openai images generate --prompt "A tiny cat" --size 1024x1536 --quality low

FILE FORMAT AND TRANSPARENCY

For GPT Image models, --output-format selects png, jpeg or webp. Transparent backgrounds require png or webp. --output-compression accepts 0 to 100 for jpeg/webp only; the API default is 100.
    openai images generate --prompt "A leaf" --background transparent --output-format webp --output-compression 80

MODEL AND MODERATION

Use an exact model ID your key can access; openai images models checks known models. GPT Image generation supports --moderation auto (default) or low.
    openai images generate --prompt "A tiny cat" --model gpt-image-2.5-flare --moderation auto

PROGRESS PREVIEWS
    openai images generate --prompt "A tiny cat" --partial-images 2
` + imageProgressSettingsHelp + `

TERMINAL PREVIEWS
    openai images generate --prompt "A tiny cat" --inline off
` + imagePreviewSettingsHelp + `

API OUTPUT
    openai --format json images generate --model gpt-image-2.5-sunburst --prompt "A tiny cat"
` + imageAPIOutputHelp

type imageOutputPlan struct {
	directory, name string
	options         []option.RequestOption
	defaults        map[string]any
	inline          string
	diagnostics     io.Writer
	partialImages   int64
	stopLoading     func()
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

func prepareImageSaving(ctx context.Context, command *cli.Command, body gjson.Result) (*imageOutputPlan, bool, error) {
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
	if streaming && command.IsSet("max-items") && command.Value("max-items").(int64) >= 0 {
		return nil, false, imageSavingFailure("--max-items could stop before the final image; use -1 or omit it when saving, or use --format json for API events.", nil)
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
	plan := &imageOutputPlan{directory: directory, name: name, defaults: make(map[string]any), partialImages: partials}
	set := func(field string, value any) {
		plan.defaults[field] = value
		plan.options = append(plan.options, option.WithJSONSet(field, value))
	}
	model := body.Get("model")
	if command.Name == "create-variation" {
		if !model.Exists() {
			set("model", "dall-e-2")
		}
		if !body.Get("response_format").Exists() {
			set("response_format", "b64_json")
		}
		if !command.IsSet("name") {
			plan.name = "image-variation"
		}
		return plan, false, nil
	}
	if !model.Exists() && !body.Get("response_format").Exists() {
		set("model", defaultSavedImageModel)
		for _, preset := range []struct {
			field string
			value any
		}{
			{"n", 1}, {"size", "auto"}, {"quality", "auto"}, {"output_format", "png"},
			{"background", "auto"}, {"moderation", "auto"}, {"partial_images", 0}, {"stream", false},
		} {
			if preset.field == "moderation" && command.Name == "edit" {
				continue
			}
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
	saved, saveErr := imageoutput.SaveResponse(ctx, response, p.directory, p.name)
	p.stopLoadingFeedback()
	previewOutput := out
	out = outputWriter{ctx: context.WithoutCancel(ctx), out: out}
	if saveErr != nil {
		message := "The API responded, but no images could be saved. Check the image data and output folder before trying again."
		if len(saved) > 0 {
			message = fmt.Sprintf("Saved %d image(s), but could not save the entire response. The listed files are kept; do not generate those images again.", len(saved))
		}
		saveErr = imageSavingFailure(message, saveErr)
	}
	for _, file := range saved {
		// Quoting keeps paths useful without allowing terminal control injection.
		// Still report completed paths if cancellation occurred on a later image.
		if _, err := fmt.Fprintf(out, "Saved image: %q\n", file.Path); err != nil {
			message := "Images were saved, but their paths could not be printed. Check the output folder before generating again."
			if saveErr != nil {
				message = fmt.Sprintf("Saved %d image(s), but could not save the entire response or print all saved paths. Check the output folder before generating again.", len(saved))
			}
			return imageSavingFailure(message, errors.Join(saveErr, err))
		}
	}
	if saveErr != nil {
		return saveErr
	}
	for _, file := range saved {
		if err := displaySavedImage(ctx, file, previewOutput, p.diagnostics, p.inline); err != nil {
			return imageSavingFailure("Images were saved, but the preview could not finish. Use the saved files; no need to generate again.", err)
		}
	}
	return nil
}
