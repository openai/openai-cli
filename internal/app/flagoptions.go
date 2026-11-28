package app

import (
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/clihelp"
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/openai/openai-cli/internal/commands/admin"
	"github.com/openai/openai-cli/internal/commands/audio"
	"github.com/openai/openai-cli/internal/commands/files"
	"github.com/openai/openai-cli/internal/commands/images"
	"github.com/openai/openai-cli/internal/commands/webhooks"
	"github.com/openai/openai-cli/internal/modeldefaults"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Temporary aliases for the generated runtime bridge. Migrate generated callers
// through their producer before removing these names.
type BodyContentType = requestconfig.BodyContentType

const (
	EmptyBody              = requestconfig.EmptyBody
	MultipartFormEncoded   = requestconfig.MultipartFormEncoded
	ApplicationJSON        = requestconfig.ApplicationJSON
	ApplicationOctetStream = requestconfig.ApplicationOctetStream
)

func FlagOptions(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,

	// This parameter is true if stdin is already in use to pass a binary parameter by using the special value
	// "-". In this case, we won't attempt to read it as a JSON/YAML blob for options setting.
	ignoreStdin bool,
	observeJSON ...func([]byte),
) (options []option.RequestOption, err error) {
	return flagOptionsWithStdin(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin, os.Stdin, observeJSON...)
}

// Keep request parsing and stdin ownership shared when a local workflow needs
// a cancellable reader. Generated callers retain their ordinary stdin reader.
func flagOptionsWithStdin(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,
	ignoreStdin bool,
	stdin io.Reader,
	observeJSON ...func([]byte),
) (options []option.RequestOption, err error) {
	stdin = files.RequestInput(cmd, stdin)
	captureOptions, err := debugCaptureOptions(cmd)
	if err != nil {
		return nil, err
	}
	if prepared, ok, err := images.ConsumeSavingRequest(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin); ok {
		return prepared, err
	}
	hooks := requestconfig.PreparationHooks{
		DebugCapture:        captureOptions,
		PreflightMultipart:  func(body map[string]any) error { return audio.PreflightVoiceBody(cmd, body) },
		ValidateCredentials: func() error { return admin.CheckCredentials(cmd) },
		PrepareResolvedInputs: func(contents requestflag.RequestContents) error {
			if err := prepareResponsesCreateInputs(cmd, contents.Body); err != nil {
				return err
			}
			return files.ValidatePurpose(cmd, contents)
		},
		ResolvedJSONOptions: func(parameters map[string]any, headers http.Header, body []byte) []option.RequestOption {
			return agentsResolvedRequestOptions(cmd, parameters, headers, body)
		},
		Encoding: requestconfig.EncodingHooks{
			PrepareMultipart: func(body map[string]any) error { return images.PrepareMultipartBody(cmd, body) },
			ValidateJSON: func(body []byte) error {
				if err := admin.ValidateExternalKeyCreate(cmd, body); err != nil {
					return err
				}
				if err := webhooks.ValidateRequestEvents(cmd, body); err != nil {
					return err
				}
				return validateProjectAPIKeyUpdate(cmd, body)
			},
			ObserveJSON:      observeJSON,
			ObserveMultipart: func(body io.Closer) { images.ObserveMultipart(cmd, body) },
		},
	}
	if audio.IsVoiceAction(cmd) {
		hooks.EmbedMultipart = func(body map[string]any, stdin *requestconfig.OnceStdinReader) (map[string]any, error) {
			return audio.EmbedVoiceBody(cmd, body, stdin)
		}
	}
	if bodyType == ApplicationJSON && isSkillCreate(cmd) {
		hooks.SelectBodyType = selectSkillZIP
		hooks.EmbedMultipart = func(body map[string]any, stdin *requestconfig.OnceStdinReader) (map[string]any, error) {
			return embedSkillZIP(cmd, body, stdin)
		}
	}
	var speechBody []byte
	speechRequest := bodyType == ApplicationJSON && audio.IsSpeechRequest(cmd)
	if speechRequest {
		hooks.Encoding.ObserveJSON = append([]func([]byte){func(body []byte) { speechBody = body }}, observeJSON...)
	}
	options, err = requestconfig.FlagOptions(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin, stdin, hooks)
	if err != nil {
		return nil, err
	}
	if speechRequest {
		return audio.SpeechModelOptions(speechBody, options, func() []option.RequestOption { return GetDefaultRequestOptions(cmd) })
	}
	if bodyType == EmptyBody && cmd.Metadata[listPagerMetadata] == true {
		// Scope the automatic size to requests that use the owning command's flags.
		// The same SDK client can also perform independently bound lookups.
		options = append(options, option.WithMiddleware(boundListRequest(cmd)))
	}
	return options, nil
}

func prepareResponsesCreateInputs(cmd *cli.Command, body any) error {
	if cmd.Name != "create" {
		return nil
	}
	resource := localcommands.ResourceName(cmd)
	if resource != "responses" && resource != "beta:responses" {
		return nil
	}
	fields, ok := body.(map[string]any)
	host := openAIBaseHost(cmd.Root())
	if !ok || host == "" {
		return nil
	}
	// GPT-6.2 Sol supports global endpoints and US and EU data residency.
	defaultModel := resource == "responses" &&
		(host == "api.openai.com" || host == "mtls.api.openai.com" || host == "us.api.openai.com" || host == "eu.api.openai.com")
	// Streaming controls cannot select a model. Other fields may select saved
	// context, or may need server validation, including malformed values.
	if len(fields) == 0 || responsesOnlyStreamingControls(fields) {
		missing := &requestconfig.MissingRequiredFlagsError{Names: []string{"model"}}
		if defaultModel {
			return &clihelp.MissingInputError{Cause: missing, Message: "Add --input to create a response with the default model."}
		}
		return missing
	}
	if defaultModel {
		applyResponsesDefaultModel(fields)
	}
	return nil
}

func applyResponsesDefaultModel(fields map[string]any) {
	// Preserve both explicit model values and any server-selected saved context.
	for _, key := range []string{"model", "prompt", "previous_response_id", "conversation"} {
		if _, supplied := fields[key]; supplied {
			return
		}
	}
	// Apply the default to fresh requests with text or input items. Leave input
	// validation and errors for other body shapes to the existing API path.
	switch input := fields["input"].(type) {
	case string:
		if strings.TrimSpace(input) == "" {
			return
		}
	case []any:
		if len(input) == 0 {
			return
		}
	default:
		return
	}
	fields["model"] = modeldefaults.Responses
}

func responsesOnlyStreamingControls(fields map[string]any) bool {
	if len(fields) > 2 {
		return false
	}
	var streaming bool
	switch stream := fields["stream"].(type) {
	case bool:
		streaming = stream
	case *bool:
		if stream == nil {
			return false
		}
		streaming = *stream
	default:
		return false
	}
	if len(fields) == 1 {
		return true
	}
	options, ok := fields["stream_options"].(map[string]any)
	if !streaming || !ok {
		return false
	}
	_, obfuscation := options["include_obfuscation"].(bool)
	return len(options) == 0 || len(options) == 1 && obfuscation
}
