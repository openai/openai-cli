package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	requestconfig "github.com/openai/openai-cli/internal/request"
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

const audioVoicesContextMetadata = "openai-audio-voices-create-context"

// Do not echo request values in validation errors.
type voiceTypeError struct{}

func (e *voiceTypeError) Error() string {
	return "Invalid voice type: --type must be audio_sample or prompt. Omit it to use audio_sample."
}

// Voice type checks require the actual request context when type is an
// uploaded scalar. Keep preparation scoped to this action, as for image uploads.
func AudioVoicesFlagOptions(ctx context.Context, cmd *cli.Command) ([]option.RequestOption, error) {
	if cmd.Metadata == nil {
		cmd.Metadata = make(map[string]any)
	}
	cmd.Metadata[audioVoicesContextMetadata] = ctx
	defer delete(cmd.Metadata, audioVoicesContextMetadata)
	return FlagOptions(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, MultipartFormEncoded, false)
}

// Check known voice requirements before opening uploads. References and readers
// retain their context-aware inspection below; protected stdin stays literal.
func preflightAudioVoicesMultipartBody(cmd *cli.Command, bodyMap map[string]any) error {
	if _, ok := cmd.Metadata[audioVoicesContextMetadata].(context.Context); !ok {
		return nil
	}
	value, supplied := bodyMap["type"]
	if !supplied {
		value = "audio_sample"
	}
	if text, ok := value.(*string); ok && text != nil {
		value = *text
	}
	switch typed := value.(type) {
	case requestconfig.UntrustedStdinValue:
		value = string(typed)
	case requestconfig.FilePathValue:
		if typed != "" {
			return requireAudioVoicesFields(cmd, bodyMap, "name")
		}
	case io.Reader:
		// Already-created readers retain late inspection and ownership cleanup.
		return nil
	default:
		// Shared expansion can resolve ordinary or named strings beginning with @.
		if text := reflect.ValueOf(value); text.Kind() == reflect.String && strings.HasPrefix(text.String(), "@") {
			return requireAudioVoicesFields(cmd, bodyMap, "name")
		}
	}
	return validateAudioVoicesRequirements(cmd, bodyMap, value)
}

// Use one type-to-requirements mapping before and after reference inspection.
func validateAudioVoicesRequirements(cmd *cli.Command, bodyMap map[string]any, voiceType any) error {
	switch voiceType {
	case "audio_sample":
		return requireAudioVoicesFields(cmd, bodyMap, "audio_sample", "consent", "name")
	case "prompt":
		return requireAudioVoicesFields(cmd, bodyMap, "name", "prompt")
	default:
		return &voiceTypeError{}
	}
}

// Inspect the embedded discriminator before expanding unrelated uploads.
// The caller owns its replay until the complete multipart body transfers ownership.
func prepareAudioVoicesMultipartBody(cmd *cli.Command, bodyMap map[string]any) error {
	ctx, ok := cmd.Metadata[audioVoicesContextMetadata].(context.Context)
	if !ok {
		return nil
	}
	value, supplied := bodyMap["type"]
	voiceType, replay, inspectErr := inspectImageMultipartSetting(ctx, "type", value)
	if supplied {
		bodyMap["type"] = replay
	} else {
		voiceType = "audio_sample"
	}
	if inspectErr != nil {
		return inspectErr
	}
	return validateAudioVoicesRequirements(cmd, bodyMap, voiceType)
}

// Share presence checks and field order between early and resolved validation.
func requireAudioVoicesFields(cmd *cli.Command, bodyMap map[string]any, required ...string) error {
	var missing []string
	for _, field := range required {
		if _, present := bodyMap[field]; !present {
			missing = append(missing, strings.ReplaceAll(field, "_", "-"))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("Required flags %q not set\nRun '%s --help' for usage information", strings.Join(missing, ", "), cmd.FullName())
	}
	return nil
}

// Resolve voice requirements before unrelated uploads can block or fail.
// Keep the type replay outside generic expansion and its partial-error cleanup.
func embedAudioVoicesMultipartBody(cmd *cli.Command, bodyMap map[string]any, stdin *requestconfig.OnceStdinReader) (body map[string]any, err error) {
	remaining := maps.Clone(bodyMap)
	typeValue, supplied := remaining["type"]
	delete(remaining, "type")
	var pendingType any
	expandingRemaining := false
	defer func() {
		if err != nil {
			if closeErr := requestconfig.CloseFileUploads(pendingType); closeErr != nil {
				err = errors.Join(err, closeErr)
			}
			if !expandingRemaining {
				// Generic expansion has not taken ownership of these existing uploads.
				if closeErr := requestconfig.CloseFileUploads(remaining); closeErr != nil {
					err = errors.Join(err, closeErr)
				}
			}
		}
	}()
	if supplied {
		// A one-field body preserves the existing --type source and file errors.
		embedded, embedErr := requestconfig.EmbedRequestFiles(cmd, map[string]any{"type": typeValue}, "body", requestconfig.EmbedIOReader, stdin)
		if embedErr != nil {
			return nil, embedErr
		}
		pendingType = embedded.(map[string]any)["type"]
		remaining["type"] = pendingType
	}
	err = prepareAudioVoicesMultipartBody(cmd, remaining)
	if supplied {
		// Inspection closes the source and replaces it with a metadata-preserving replay.
		pendingType = remaining["type"]
	}
	delete(remaining, "type")
	if err != nil {
		return nil, err
	}
	expandingRemaining = true // Generic expansion cleans its visited partial result.
	embedded, err := requestconfig.EmbedRequestFiles(cmd, remaining, "body", requestconfig.EmbedIOReader, stdin)
	if err != nil {
		return nil, err
	}
	body = embedded.(map[string]any)
	if supplied {
		body["type"] = pendingType
	}
	return body, nil
}

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
	if prepared, ok, err := consumeImageSavingRequest(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin); ok {
		return prepared, err
	}
	hooks := requestconfig.PreparationHooks{
		PreflightMultipart:  func(body map[string]any) error { return preflightAudioVoicesMultipartBody(cmd, body) },
		ValidateCredentials: func() error { return checkAdminCredentials(cmd) },
		Encoding: requestconfig.EncodingHooks{
			PrepareMultipart: func(body map[string]any) error { return prepareImageMultipartBody(cmd, body) },
			ValidateJSON:     func(body []byte) error { return validateProjectAPIKeyUpdate(cmd, body) },
			ObserveJSON:      observeJSON,
			ObserveMultipart: func(body io.Closer) {
				// Look up the feature state only when the encoded body is ready.
				if state, ok := cmd.Metadata[imageMultipartMetadata].(*imageMultipartPreparation); ok {
					state.body = body
				}
			},
		},
	}
	if _, voiceAction := cmd.Metadata[audioVoicesContextMetadata].(context.Context); voiceAction {
		hooks.EmbedMultipart = func(body map[string]any, stdin *requestconfig.OnceStdinReader) (map[string]any, error) {
			return embedAudioVoicesMultipartBody(cmd, body, stdin)
		}
	}
	return requestconfig.FlagOptions(cmd, nestedFormat, arrayFormat, bodyType, ignoreStdin, stdin, hooks)
}
