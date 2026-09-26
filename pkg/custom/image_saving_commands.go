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
const imageSavingRequestMetadata = "openai-image-saving-request"

// Decorate the generated image commands with shared saving behavior.
// Model discovery and terminal rendering have separate owners.
func configureImageSaving(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	for _, name := range []string{"generate", "edit", "create-variation"} {
		command := images.Command(name)
		if command == nil {
			continue
		}
		registerImageSavingFlags(command)
		if name == "generate" {
			command.Description = imageGenerationSavingHelp
			command.CustomHelpTemplate = imageGenerationQuickHelp
		} else {
			command.Description = imageUploadSavingHelp
			command.CustomHelpTemplate = imageUploadQuickHelp
		}
		command.Action = imageSavingWorkflow(command.Action)
	}
}

func registerImageSavingFlags(command *cli.Command) {
	command.Flags = append(command.Flags,
		&cli.StringFlag{Name: "output-dir", Usage: "Save images in an existing `DIRECTORY`", DefaultText: "~/Downloads/gpt-images/"},
		&cli.StringFlag{Name: "inline", Value: "auto", Usage: "Show a saved-image preview: auto, on or off; on allows a local Apple Terminal image font"},
		&cli.StringFlag{Name: "name", Usage: "Save with this filename `STEM` (an image extension is optional); existing files are kept"},
	)
	model := defaultSavedImageModel
	if command.Name == "create-variation" {
		model = "dall-e-2"
	}
	for _, flag := range command.Flags {
		switch flag := flag.(type) {
		case *requestflag.Flag[*int64]:
			if flag.Name == "n" {
				flag.Aliases = append(flag.Aliases, "count")
			}
		case *requestflag.Flag[*string]:
			if flag.Name == "model" {
				flag.Usage = "CLI saving default: " + model + ". Explicit models retain API defaults. API behavior: " + flag.Usage
				flag.HideDefault = true
			}
			if flag.Name == "response-format" {
				flag.Usage = "CLI saving requests b64_json for DALL-E when omitted; url disables saving. API behavior: " + flag.Usage
				flag.HideDefault = true
			}
		}
	}
}

type preparedImageSavingRequest struct {
	options  []option.RequestOption
	consumed bool
	bodyType BodyContentType
}

type imagePresentationKey struct{}
type imagePresentation struct {
	plan   *imageOutputPlan
	writer io.Writer
}

// Prepare once so stdin and @file values are not consumed twice. The generated
// handler still owns SDK dispatch and response/stream lifetime.
func imageSavingWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		inline := command.String("inline")
		if inline != "auto" && inline != "on" && inline != "off" {
			return imageSavingFailure("--inline must be auto, on or off.", nil)
		}
		if command.Metadata == nil {
			command.Metadata = make(map[string]any)
		}
		if _, exists := command.Metadata[imageSavingRequestMetadata]; exists {
			return errors.New("image request is already being prepared")
		}
		var options []option.RequestOption
		var plan *imageOutputPlan
		var streaming bool
		var err error
		bodyType := ApplicationJSON
		if command.Name != "generate" {
			if command.Args().Len() != 0 {
				return imageSavingFailure("Unexpected extra arguments. Put source filenames after --image; use --help for usage.", nil)
			}
			bodyType = MultipartFormEncoded
			state := &imageMultipartPreparation{context: ctx}
			// Prepared uploads still need an owner if the generated action fails
			// before dispatch. Close is idempotent after the SDK has used the body.
			defer func() {
				if state.body != nil {
					_ = state.body.Close()
				}
			}()
			command.Metadata[imageMultipartMetadata] = state
			defer delete(command.Metadata, imageMultipartMetadata)
			options, err = FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, bodyType, false)
			plan, streaming = state.plan, state.streaming
		} else {
			var body gjson.Result
			options, err = FlagOptions(command, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, bodyType, false,
				func(raw []byte) { body = gjson.ParseBytes(raw) })
			if err == nil {
				plan, streaming, err = prepareImageSaving(ctx, command, body)
			}
			if err == nil && plan != nil {
				options = append(options, plan.options...)
			}
		}
		if err != nil {
			return err
		}
		if plan != nil {
			plan.inline = inline
			// The generated root ErrWriter buffers failures for main. Preview
			// warnings accompany a successful save and must reach stderr now.
			plan.diagnostics = os.Stderr
		}
		restore, err := selectImageGenerationStream(command, streaming)
		if err != nil {
			return err
		}
		defer restore()
		command.Metadata[imageSavingRequestMetadata] = &preparedImageSavingRequest{options: options, bodyType: bodyType}
		defer delete(command.Metadata, imageSavingRequestMetadata)
		ctx = context.WithValue(ctx, imagePresentationKey{}, imagePresentation{plan, command.Root().Writer})
		return next(ctx, command)
	}
}

func consumeImageSavingRequest(command *cli.Command, nested apiquery.NestedQueryFormat, array apiquery.ArrayQueryFormat, body BodyContentType, ignoreStdin bool) ([]option.RequestOption, bool, error) {
	prepared, ok := command.Metadata[imageSavingRequestMetadata].(*preparedImageSavingRequest)
	if !ok {
		return nil, false, nil
	}
	if prepared.consumed || nested != apiquery.NestedQueryFormatBrackets || array != apiquery.ArrayQueryFormatBrackets || body != prepared.bodyType || ignoreStdin {
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
	if opts.OutputKind != kind || opts.Context == nil {
		return imagePresentation{}, false
	}
	switch opts.Operation {
	case imageGenerationOperation, "(resource) images > (method) edit", "(resource) images > (method) create_variation":
	default:
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
