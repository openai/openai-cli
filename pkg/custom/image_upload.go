package custom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

const imageMultipartMetadata = "openai-image-multipart-preparation"

type imageMultipartPreparation struct {
	context   context.Context
	body      gjson.Result
	plan      *imageOutputPlan
	streaming bool
}

func imageMultipartCommand(command *cli.Command) bool {
	enabled, _ := command.Metadata["image-upload"].(bool)
	return enabled
}

// Called after trusted file expansion, before the multipart body takes ownership
// of its upload readers. Only scalar image settings are inspected; image bytes
// keep using the shared streaming multipart encoder and its cleanup rules.
func prepareImageMultipartBody(command *cli.Command, body map[string]any) error {
	state, ok := command.Metadata[imageMultipartMetadata].(*imageMultipartPreparation)
	if !ok {
		return nil
	}
	settings := make(map[string]any)
	for _, name := range []string{"prompt", "model", "n", "size", "quality", "output_format", "background", "response_format", "partial_images", "stream"} {
		if value, found := body[name]; found {
			inspected, replay, err := inspectImageMultipartSetting(state.context, name, value)
			body[name] = replay
			if err != nil {
				return err
			}
			settings[name] = inspected
		}
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return errors.New("could not read image settings")
	}
	state.body = gjson.ParseBytes(raw)
	if command.Name == "create-variation" && (state.body.Get("stream").Bool() || state.body.Get("partial_images").Int() > 0) {
		return errors.New("image variations do not support streaming or progress previews; remove stream and partial_images from your input")
	}
	state.plan, state.streaming, err = prepareImageRequestOutput(state.context, command, state.body)
	if err != nil {
		return err
	}
	if state.plan != nil {
		for name, value := range state.plan.defaults {
			body[name] = value
		}
		if state.streaming {
			body["stream"] = true
		}
	}
	return nil
}

// Scalar @file references arrive as upload readers after shared trusted-input
// expansion. Inspect their contents once and replay the exact original bytes,
// retaining multipart filenames/content types. Source image and mask readers
// never enter this path and stay streamed. There is no new file-size limit.
func inspectImageMultipartSetting(ctx context.Context, name string, value any) (any, any, error) {
	reader, ok := value.(io.Reader)
	if !ok {
		return value, value, nil
	}
	upload, owned := value.(fileUpload)
	var closeOnce sync.Once
	var closeErr error
	closeSource := func() {
		if owned {
			closeOnce.Do(func() { closeErr = upload.Close() })
		}
	}
	cancelDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(cancelDone); closeSource() })
	data, readErr := io.ReadAll(imageSettingReader{ctx, reader})
	if !stop() {
		<-cancelDone
	}
	closeSource()
	var replay any = bytes.NewReader(data)
	if owned {
		upload.Reader = bytes.NewReader(data)
		upload.size, upload.knownSize = int64(len(data)), true
		replay = upload
	}
	if err := ctx.Err(); err != nil {
		return nil, replay, err
	}
	if readErr != nil || closeErr != nil {
		// Reader errors may quote file contents. Keep only the known option name.
		return nil, replay, fmt.Errorf("could not read --%s from its file", strings.ReplaceAll(name, "_", "-"))
	}
	var inspected any = string(data)
	if name == "n" || name == "partial_images" || name == "stream" {
		parsed := gjson.ParseBytes(data)
		if gjson.ValidBytes(data) && (parsed.Type == gjson.Number || parsed.Type == gjson.True || parsed.Type == gjson.False || parsed.Type == gjson.Null) {
			inspected = json.RawMessage(data)
		}
	}
	return inspected, replay, nil
}

type imageSettingReader struct {
	context context.Context
	source  io.Reader
}

func (r imageSettingReader) Read(buffer []byte) (int, error) {
	if err := r.context.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(buffer)
}

func registerImageUpload(command *cli.Command) {
	if command.Metadata == nil {
		command.Metadata = make(map[string]any)
	}
	command.Metadata["image-upload"] = true
	command.Metadata["image-reference-flag"] = renderImageReferenceFlag
	command.Metadata["local-help-full"] = imageUploadFullHelp
	command.CustomHelpTemplate = imageUploadQuickHelp
	command.OnUsageError = imageGenerateUsageError
	registerImageSavingFlags(command)
	command.Action = imageUploadAction(imageGenerateWorkflow(command.Action))
	// Keep generated model compatibility descriptions after the endpoint's preset.
	model := defaultSavedImageModel
	if command.Name == "create-variation" {
		model = "dall-e-2"
	}
	for _, candidate := range command.Flags {
		if flag, ok := candidate.(*requestflag.Flag[*string]); ok && flag.Name == "model" {
			flag.Usage = strings.Replace(flag.Usage, "CLI saving preset: "+defaultSavedImageModel+" (see DEFAULTS WHEN SAVING).", "CLI saving preset: "+model+".", 1)
		}
	}
	if command.Name == "create-variation" {
		command.Description = "Creates a variation using dall-e-2. Saves to ~/Downloads/gpt-images/ automatically. Existing files and source images are kept. Use --format json for API data without saving."
	} else {
		command.Description = "Edits source images using " + defaultSavedImageModel + " by default when saving. Names the saved image from your prompt in ~/Downloads/gpt-images/. Existing files and source images are kept. Add --partial-images 1, 2 or 3 for progress previews; only the final image is saved. Use --format json for API data without saving."
	}
}

func imageUploadAction(next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		if command.Args().Len() != 0 {
			return fmt.Errorf("Put the source filename after --image. Help: %s images %s --help", imageHelpInvocation(command), command.Name)
		}
		return next(ctx, command)
	}
}

const imageUploadQuickHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}{{if eq .Name "edit"}}Edit an image
  {{$bin}} images edit --image "photo.png" --prompt "Make the sky purple"

Default model: ` + defaultSavedImageModel + `.
{{else}}Make a variation of an image
  {{$bin}} images create-variation --image "photo.png"

Uses dall-e-2. The source must be a square PNG under 4 MB.
{{end}}Saves a new image to ~/Downloads/gpt-images/ automatically.
Keeps your source file and existing images. Shows a preview where supported.

Optional: add one of these to the command above.
  --name result                   Save as result.png (existing files kept)
  --output-dir "~/Downloads"       Save to an existing folder
  --count 2                       Make 2 images (choose 1 to 10)
  --open                          Open the saved image in a separate window
  --inline off                    Save without an inline preview

{{if eq .Name "edit"}}Repeat --image to use multiple source files. Add --mask "mask.png" to limit edits.
{{end}}Every option: {{$bin}} help --all images {{.Name}}
Scripts: add --format json for API data without saving.
`

const imageUploadFullHelp = `{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Images {{.Name}}: full reference

{{.Description}}

SAVE AND VIEW
  --output-dir chooses an existing folder. The default folder is created automatically.
  --name supplies a filename, without a directory. Actual image bytes choose its extension.
  Names already in use get -2, -3, etc. Every saved path is printed.
  --open opens the saved original. --inline on/off overrides your preview preference.
  Explicit data formats, --transform and --raw-output skip saving.
  --response-format url also returns API data; it cannot be combined with saving flags.

IMAGE OPTIONS
{{range .VisibleFlags}}{{call (index $.Metadata "image-reference-flag") .}}{{end}}
GLOBAL OPTIONS
{{range .VisiblePersistentFlags}}{{call (index $.Metadata "image-reference-flag") .}}{{end}}`
