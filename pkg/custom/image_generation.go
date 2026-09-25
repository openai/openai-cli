package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const imageGenerationOperation = "(resource) images > (method) generate"
const imageGenerationRequestMetadata = "openai-image-generation-request"

// Decorate only generation after the generated tree is assembled. Editing,
// variations, model discovery and rendering have separate owners.
func configureImageGeneration(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	generate := images.Command("generate")
	if generate == nil {
		return
	}
	generate.Flags = append(generate.Flags,
		&cli.StringFlag{Name: "output-dir", Usage: "Save images in an existing `DIRECTORY`", DefaultText: "~/Downloads/gpt-images/"},
		&cli.StringFlag{Name: "name", Usage: "Save with this filename `STEM` (an image extension is optional); existing files are kept"},
	)
	for _, flag := range generate.Flags {
		switch flag := flag.(type) {
		case *requestflag.Flag[*int64]:
			if flag.Name == "n" {
				flag.Aliases = append(flag.Aliases, "count")
			}
		case *requestflag.Flag[*string]:
			if flag.Name == "model" {
				flag.Usage = "CLI saving default: " + defaultSavedImageModel + ". Explicit models retain API defaults. API behavior: " + flag.Usage
				flag.HideDefault = true
			}
			if flag.Name == "response-format" {
				flag.Usage = "CLI saving requests b64_json for an explicit DALL-E model when omitted; url disables saving. API behavior: " + flag.Usage
				flag.HideDefault = true
			}
		}
	}
	generate.Description = imageGenerationSavingHelp
	generate.CustomHelpTemplate = imageGenerationQuickHelp
	generate.Action = imageGenerationWorkflow(generate.Action)
}

type preparedImageGenerationRequest struct {
	options  []option.RequestOption
	consumed bool
}

type imagePresentationKey struct{}
type imagePresentation struct {
	plan   *imageOutputPlan
	writer io.Writer
}

// Prepare once so stdin and @file values are not consumed twice. The generated
// handler still owns SDK dispatch and response/stream lifetime.
func imageGenerationWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		if command.Metadata == nil {
			command.Metadata = make(map[string]any)
		}
		if _, exists := command.Metadata[imageGenerationRequestMetadata]; exists {
			return errors.New("image request is already being prepared")
		}
		var body gjson.Result
		options, err := FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, ApplicationJSON, false,
			func(raw []byte) { body = gjson.ParseBytes(raw) })
		if err != nil {
			return err
		}
		plan, streaming, err := prepareImageGeneration(ctx, command, body)
		if err != nil {
			return err
		}
		restore, err := selectImageGenerationStream(command, streaming)
		if err != nil {
			return err
		}
		defer restore()
		if plan != nil {
			options = append(options, plan.options...)
		}
		command.Metadata[imageGenerationRequestMetadata] = &preparedImageGenerationRequest{options: options}
		defer delete(command.Metadata, imageGenerationRequestMetadata)
		ctx = context.WithValue(ctx, imagePresentationKey{}, imagePresentation{plan, command.Root().Writer})
		return next(ctx, command)
	}
}

func consumeImageGenerationRequest(command *cli.Command, nested apiquery.NestedQueryFormat, array apiquery.ArrayQueryFormat, body BodyContentType, ignoreStdin bool) ([]option.RequestOption, bool, error) {
	prepared, ok := command.Metadata[imageGenerationRequestMetadata].(*preparedImageGenerationRequest)
	if !ok {
		return nil, false, nil
	}
	if prepared.consumed || nested != apiquery.NestedQueryFormatBrackets || array != apiquery.ArrayQueryFormatBrackets || body != ApplicationJSON || ignoreStdin {
		return nil, true, errors.New("image request preparation does not match the generated action")
	}
	prepared.consumed = true
	return prepared.options, true, nil
}

// The generated action selects streaming from a flag, but stdin also supplies
// request fields. Change only that selector and restore its original set state.
func selectImageGenerationStream(command *cli.Command, streaming bool) (func(), error) {
	current, _ := command.Value("stream").(*bool)
	if (current != nil && *current) == streaming {
		return func() {}, nil
	}
	for _, candidate := range command.Flags {
		flag, ok := candidate.(*requestflag.Flag[*bool])
		if !ok || flag.Name != "stream" {
			continue
		}
		original, replacement := *flag, *flag
		if err := replacement.PreParse(); err != nil {
			return nil, err
		}
		if err := replacement.Set("stream", strconv.FormatBool(streaming)); err != nil {
			return nil, err
		}
		*flag = replacement
		return func() { *flag = original }, nil
	}
	return nil, errors.New("image command has no compatible stream selector")
}

func savedImagePresentation(opts ShowJSONOpts, kind OutputKind) (imagePresentation, bool) {
	if opts.Operation != imageGenerationOperation || opts.OutputKind != kind {
		return imagePresentation{}, false
	}
	presentation, ok := opts.Context.Value(imagePresentationKey{}).(imagePresentation)
	return presentation, ok && presentation.plan != nil
}

func (p imagePresentation) output(write func(io.Writer) error) error {
	out := p.writer
	if out == nil {
		out = os.Stdout
	}
	if stdout, ok := out.(*os.File); ok && stdout == os.Stdout {
		// Use the existing SIGPIPE protection, retaining saving errors even when
		// reporting a completed path also encounters a closed pipe.
		var result error
		_ = streamToStdout(func(*os.File) error { result = write(out); return nil })
		return result
	}
	return write(out)
}
