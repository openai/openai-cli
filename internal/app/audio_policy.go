package app

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3"
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
	audio.ConfigureLiveTranslation(root, audio.TranslationOptions{
		DefaultRequestOptions: GetDefaultRequestOptions,
		RequestOptions: func(command *cli.Command) ([]option.RequestOption, error) {
			// Live sessions do not consume the file-translation body or stdin.
			// Keep root headers, query options, credential checks, and transport policy.
			view := *command
			view.Flags = nil
			return FlagOptions(&view, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, true)
		},
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
		Raw:                    opts.Transform == "" && (strings.EqualFold(opts.Format, "raw") || opts.RawOutput),
		AuthenticationGuidance: speechAuthenticationGuidance,
		Render: func(ctx context.Context, stdout io.Writer, iter jsonview.Iterator[clioutput.JSON]) error {
			current := opts
			current.Context, current.Stdout = ctx, stdout
			return ShowJSONIterator(iter, -1, current)
		},
	}
}

// Reuse the request policy's value-free guidance for the interactive error panel.
func speechAuthenticationGuidance(command *cli.Command, apierr *openai.Error) audio.AuthenticationGuidance {
	if command == nil || apierr == nil || apierr.StatusCode != http.StatusUnauthorized && apierr.StatusCode != http.StatusForbidden {
		return audio.AuthenticationGuidance{}
	}
	root := command.Root()
	custom := apiAuthenticationCustomBase(root)
	failure := withCommandError(command, apierr)
	_, override := apiAuthenticationCredentialSource(root, "api-key", "OPENAI_API_KEY", "API key")
	guidance := audio.AuthenticationGuidance{}
	if apierr.StatusCode == http.StatusUnauthorized && apierr.Code == "ip_not_authorized" {
		guidance.Summary = "This IP address is not allowed."
		guidance.Action = "Ask an administrator to check your outbound IP in the allowlist."
		return guidance
	}
	if readableAPIAccessPermissionMessage(root, failure, apierr) != "" {
		guidance.Summary = "Required permissions are missing."
		switch {
		case custom:
			guidance.Action = "Check this credential's permissions for this API endpoint."
		case override:
			guidance.Action = "Check the permissions of the Authorization credential."
		default:
			guidance.Action = "Check your organization and project roles, and the key's permissions."
		}
		return guidance
	}
	setupStep := "In your main terminal, run: " + clihelp.DisplayInvocation(root) + " help setup"
	switch {
	case override:
		guidance.Action = "Correct or remove the Authorization override."
	case root.IsSet("api-key") && root.Count("api-key") > 0 && root.String("api-key") == "":
		guidance.Summary = "The API key override is empty."
		guidance.Action = "Close this form."
		if !custom && os.Getenv("OPENAI_API_KEY") == "" {
			guidance.Action += "\n" + setupStep
		}
		guidance.Action += "\nReopen without the empty --api-key option."
	case custom:
		guidance.Summary = "Authentication failed for this API endpoint."
		if apierr.StatusCode == http.StatusForbidden {
			guidance.Summary = "This API endpoint denied access."
		}
		guidance.Action = "Check the credential required by this API endpoint."
	case apierr.StatusCode == http.StatusForbidden:
		guidance.Action = "Check your key's permissions and selected project."
	case !apiAuthenticationKeyPresent(root, "api-key", "OPENAI_API_KEY"):
		guidance.Summary = "No API key is configured."
		guidance.Action = setupStep
	case apierr.Code == "invalid_api_key":
		guidance.Action = "Replace the rejected API key.\nReopen this form."
	default:
		guidance.Action = "Check your API key and selected project.\nReopen this form."
	}
	return guidance
}
