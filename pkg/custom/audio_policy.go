package custom

import (
	"context"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func configureAudioVoices(root *cli.Command) {
	audio.ConfigureVoices(root, audio.VoiceOptions{
		DefaultRequestOptions: GetDefaultRequestOptions,
		RequestOptions: func(cmd *cli.Command) ([]option.RequestOption, error) {
			return FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, MultipartFormEncoded, false)
		},
		ShowResponse: func(ctx context.Context, cmd *cli.Command, value gjson.Result) error {
			return ShowJSON(value, ShowJSONOpts{
				Context: ctx, Operation: "(resource) audio.voices > (method) create",
				OutputKind: OutputResponse, ExplicitFormat: cmd.Root().IsSet("format"),
				Format: cmd.Root().String("format"), RawOutput: cmd.Root().Bool("raw-output"),
				Title: "audio:voices create", Transform: cmd.Root().String("transform"),
			})
		},
	})
}

func registerAudioCommands(root *cli.Command) {
	audio.RegisterLocal(root, audio.LocalOptions{
		ShowData: func(ctx context.Context, command *cli.Command, value gjson.Result) error {
			root := command.Root()
			return ShowJSON(value, ShowJSONOpts{Context: ctx, Format: strings.ToLower(root.String("format")), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"), Transform: root.String("transform"), Stdout: root.Writer, Stderr: root.ErrWriter, Title: "audio " + command.Name})
		},
		ReportReceipt: ReportSaveReceipt,
	})
}

func configureReadableSpeech(root *cli.Command) {
	audio.ConfigureSpeech(root, func(ctx context.Context, command *cli.Command) audio.SpeechPresentation {
		root := command.Root()
		opts := ShowJSONOpts{
			Context: ctx, Operation: "(resource) audio.speech > (method) create",
			OutputKind: OutputStreamEvent, ExplicitFormat: root.IsSet("format"),
			Format: root.String("format"), RawOutput: root.Bool("raw-output"),
			Transform: root.String("transform"), Title: "audio:speech create",
		}
		return speechPresentation(opts)
	})
}

func speechPresentation(opts ShowJSONOpts) audio.SpeechPresentation {
	return audio.SpeechPresentation{
		Raw: opts.Transform == "" && (strings.EqualFold(opts.Format, "raw") || opts.RawOutput),
		Render: func(ctx context.Context, stdout io.Writer, iter jsonview.Iterator[clioutput.JSON]) error {
			current := opts
			current.Context, current.Stdout = ctx, stdout
			return ShowJSONIterator(iter, -1, current)
		},
	}
}
