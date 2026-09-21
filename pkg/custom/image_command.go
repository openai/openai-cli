package custom

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

const imageRequestMetadata = "openai-image-prepared-request"

// All image command decoration happens after the generated tree is assembled.
// Neither this package nor the transformers package imports generated commands.
func configureImageCommands(root *cli.Command) {
	images := root.Command("images")
	if images == nil {
		return
	}
	if generate := images.Command("generate"); generate != nil {
		registerImageGenerate(generate)
	}
	for _, name := range []string{"edit", "create-variation"} {
		if command := images.Command(name); command != nil {
			registerImageUpload(command)
		}
	}
	registerImageInline(root)
	registerImagePreview(root)
	registerImageModels(root)
	registerImageOptions(root)
}

func isImageGenerateCommand(command *cli.Command) bool {
	configured, _ := command.Metadata["image-generate"].(bool)
	return configured
}

type preparedImageRequest struct {
	options   []option.RequestOption
	consumed  bool
	multipart bool
}

type imagePresentationKey struct{}
type imagePresentation struct {
	plan   *imageOutputPlan
	writer io.Writer
}

// imageGenerateWorkflow prepares the request once, then delegates the API call
// to the generated action. Its FlagOptions call consumes these prepared options
// instead of reopening files or reading stdin a second time.
func imageGenerateWorkflow(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		if command.Metadata == nil {
			command.Metadata = make(map[string]any)
		}
		if _, exists := command.Metadata[imageRequestMetadata]; exists {
			return errors.New("image request is already being prepared")
		}
		options, plan, streaming, err := imageGenerateOptions(ctx, command)
		if err != nil {
			return err
		}
		restoreStream, err := selectImageStream(command, streaming)
		if err != nil {
			return err
		}
		defer restoreStream()
		command.Metadata[imageRequestMetadata] = &preparedImageRequest{options: options, multipart: imageMultipartCommand(command)}
		defer delete(command.Metadata, imageRequestMetadata)
		if plan != nil {
			ctx = transformers.WithImageOutput(ctx)
			ctx = context.WithValue(ctx, imagePresentationKey{}, imagePresentation{plan, command.Root().Writer})
		}
		return next(ctx, command)
	}
}

// Preserve the original parsed flag, including its explicit/unset state. Only
// the generated handler's stream selector changes while its action is running.
func selectImageStream(command *cli.Command, streaming bool) (func(), error) {
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

func consumeImageRequest(command *cli.Command, nested apiquery.NestedQueryFormat, array apiquery.ArrayQueryFormat, body BodyContentType, ignoreStdin bool) ([]option.RequestOption, bool, error) {
	prepared, ok := command.Metadata[imageRequestMetadata].(*preparedImageRequest)
	if !ok {
		return nil, false, nil
	}
	expectedBody := ApplicationJSON
	if prepared.multipart {
		expectedBody = MultipartFormEncoded
	}
	if prepared.consumed || nested != apiquery.NestedQueryFormatBrackets || array != apiquery.ArrayQueryFormatBrackets || body != expectedBody || ignoreStdin {
		return nil, true, errors.New("image request preparation does not match the generated action")
	}
	prepared.consumed = true
	return prepared.options, true, nil
}

func imagePresentationFor(opts ShowJSONOpts, kind OutputKind) (imagePresentation, bool) {
	if opts.Context == nil || !transformers.IsImageOperation(opts.Operation) || opts.OutputKind != kind {
		return imagePresentation{}, false
	}
	presentation, ok := opts.Context.Value(imagePresentationKey{}).(imagePresentation)
	return presentation, ok && presentation.plan != nil
}
