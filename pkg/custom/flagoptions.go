package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/apiform"
	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/debugmiddleware"
	requestconfig "github.com/openai/openai-cli/internal/request"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"

	"github.com/urfave/cli/v3"
)

type BodyContentType int

const (
	EmptyBody BodyContentType = iota
	MultipartFormEncoded
	ApplicationJSON
	ApplicationOctetStream
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
	// Validate literal headers before reading any request input. Flag validators
	// include the supplied value in errors, which can expose credentials.
	headers, err := requestconfig.Headers(cmd)
	if err != nil {
		return nil, err
	}
	headerOptions := make([]option.RequestOption, 0, len(headers))
	for name, values := range headers {
		headerOptions = append(headerOptions, option.WithHeader(name, values[0]))
	}

	stdinSecurity, err := requestconfig.NewStdinSecurity()
	if err != nil {
		return nil, err
	}

	// Once multipart files have been opened, close them on every error path
	// until ownership is transferred to the multipart request body below.
	var pendingMultipartUploads any
	defer func() {
		if err != nil && pendingMultipartUploads != nil {
			err = errors.Join(err, requestconfig.CloseFileUploads(pendingMultipartUploads))
		}
	}()

	if cmd.Bool("debug") {
		sensitiveHeaders := slices.Collect(maps.Keys(headers))
		// The SDK reads environment headers. Match its name parsing so these
		// values receive the same redaction as explicit header flags.
		for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
			if name, _, ok := strings.Cut(line, ":"); ok {
				sensitiveHeaders = append(sensitiveHeaders, strings.TrimSpace(name))
			}
		}
		options = append(options, option.WithMiddleware(debugmiddleware.NewRequestLogger(sensitiveHeaders...).Middleware()))
	}

	requestContents := requestflag.ExtractRequestContents(cmd)

	// Translate inner-field aliases in YAML values that came from flags (e.g.
	// `--parent '{"alias": val}'` resolving to the canonical inner field).
	if bodyMap, ok := requestContents.Body.(map[string]any); ok {
		requestconfig.ApplyDataAliases(cmd, bodyMap)
	}
	// Resolve explicit consumers before attempting automatic whole-request input.
	// FileInput values must retain their literal path semantics during inspection.
	if err := requestconfig.WrapFileInputValues(cmd, &requestContents, stdinSecurity); err != nil {
		return nil, err
	}
	explicitStdin, err := requestconfig.StdinConsumers(requestContents, ignoreStdin)
	if err != nil {
		return nil, err
	}

	stdinConsumedByPipe := false
	if bodyType != ApplicationOctetStream && !ignoreStdin && explicitStdin == 0 && isInputPiped() {
		pipeData, err := io.ReadAll(stdin)
		if err != nil {
			return nil, err
		}

		if len(pipeData) > 0 {
			stdinConsumedByPipe = true
			var bodyData any
			if err := requestflag.UnmarshalYAMLOrJSON(pipeData, &bodyData); err != nil {
				return nil, fmt.Errorf("Failed to parse piped data as YAML/JSON:\n%w", err)
			}
			if bodyMap, ok := bodyData.(map[string]any); ok {
				requestconfig.ApplyDataAliases(cmd, bodyMap)
				stdinSecurity.RecordBodyFlags(cmd, bodyMap)
				// Apply any matching keys from the piped data to path, query, and header flags
				// that have not already been set via the command line.
				if err := requestflag.ApplyStdinDataToFlagsWithProvenance(cmd, bodyMap, stdinSecurity.RecordFlag); err != nil {
					return nil, err
				}
				// Re-extract request contents now that flags may have been updated.
				requestContents = requestflag.ExtractRequestContents(cmd)
				stdinSecurity.ProtectFlagValues(&requestContents)
				if stdinSecurity.Enabled() {
					bodyMap = requestconfig.ProtectStdinValue(bodyMap).(map[string]any)
				}
				// Remove keys that were consumed as query, header, or path params so they
				// don't also leak into the request body via the maps.Copy merge below.
				// We delete both the canonical key and any aliases since the user may have
				// piped data using an alias name rather than the canonical API name.
				for _, flag := range cmd.Flags {
					inReq, ok := flag.(requestflag.InRequest)
					if !ok || !flag.IsSet() {
						continue
					}
					if inReq.GetQueryPath() != "" || inReq.GetHeaderPath() != "" || inReq.GetPathParam() != "" {
						delete(bodyMap, inReq.GetQueryPath())
						delete(bodyMap, inReq.GetHeaderPath())
						delete(bodyMap, inReq.GetPathParam())
						for _, alias := range inReq.GetDataAliases() {
							delete(bodyMap, alias)
						}
					}
				}
				if bodyType != EmptyBody {
					if flagMap, ok := requestContents.Body.(map[string]any); ok {
						maps.Copy(bodyMap, flagMap)
						requestContents.Body = bodyMap
					} else {
						bodyData = requestContents.Body
					}
				}
			} else if bodyType != EmptyBody {
				if stdinSecurity.Enabled() {
					bodyData = requestconfig.ProtectStdinValue(bodyData)
				}
				if flagMap, ok := requestContents.Body.(map[string]any); ok && len(flagMap) > 0 {
					return nil, fmt.Errorf("Cannot merge flags with a body that is not a map: %v", bodyData)
				} else {
					requestContents.Body = bodyData
				}
			}
		}
	}

	if missingFlags := requestflag.GetMissingRequiredFlags(cmd, requestContents.Body); len(missingFlags) > 0 {
		if len(missingFlags) == 1 {
			return nil, fmt.Errorf("Required flag %q not set\nRun '%s --help' for usage information", missingFlags[0].Names()[0], cmd.FullName())
		} else {
			names := []string{}
			for _, flag := range missingFlags {
				names = append(names, flag.Names()[0])
			}
			return nil, fmt.Errorf("Required flags %q not set\nRun '%s --help' for usage information", strings.Join(names, ", "), cmd.FullName())
		}
	}

	if bodyType == MultipartFormEncoded {
		if bodyMap, ok := requestContents.Body.(map[string]any); ok {
			if err := preflightAudioVoicesMultipartBody(cmd, bodyMap); err != nil {
				return nil, err
			}
		}
	}

	if err := checkAdminCredentials(cmd); err != nil {
		return nil, err
	}

	// FileInput values are file paths, so wrap trusted values with FilePathValue
	// for automatic expansion. In untrusted-stdin mode, reject piped values.
	if err := requestconfig.WrapFileInputValues(cmd, &requestContents, stdinSecurity); err != nil {
		return nil, err
	}
	if _, err := requestconfig.StdinConsumers(requestContents, ignoreStdin); err != nil {
		return nil, err
	}

	// Determine stdin availability for FileInput params that use "-".
	var stdinReader requestconfig.OnceStdinReader
	if ignoreStdin {
		stdinReader = requestconfig.OnceStdinReader{FailureReason: "stdin is already being used for the request body"}
	} else if stdinConsumedByPipe {
		stdinReader = requestconfig.OnceStdinReader{FailureReason: "stdin was already consumed by piped YAML/JSON input"}
	} else {
		stdinReader = requestconfig.OnceStdinReader{Reader: stdin}
	}

	// Embed files passed as "@file.jpg" in the request body, headers, and query:
	embedStyle := requestconfig.EmbedText
	if bodyType == ApplicationOctetStream || bodyType == MultipartFormEncoded {
		embedStyle = requestconfig.EmbedIOReader
	}

	var embedded any
	bodyMap, bodyIsMap := requestContents.Body.(map[string]any)
	_, voiceAction := cmd.Metadata[audioVoicesContextMetadata].(context.Context)
	if bodyType == MultipartFormEncoded && bodyIsMap && voiceAction {
		embedded, err = embedAudioVoicesMultipartBody(cmd, bodyMap, &stdinReader)
	} else {
		embedded, err = requestconfig.EmbedRequestFiles(cmd, requestContents.Body, "body", embedStyle, &stdinReader)
	}
	if err != nil {
		return nil, err
	}
	requestContents.Body = embedded
	if bodyType == MultipartFormEncoded {
		pendingMultipartUploads = embedded
	}

	if headersWithFiles, err := requestconfig.EmbedRequestFiles(cmd, requestContents.Headers, "header", requestconfig.EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Headers = headersWithFiles.(map[string]any)
	}
	if queriesWithFiles, err := requestconfig.EmbedRequestFiles(cmd, requestContents.Queries, "query", requestconfig.EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Queries = queriesWithFiles.(map[string]any)
	}

	querySettings := apiquery.QuerySettings{
		NestedFormat: nestedFormat,
		ArrayFormat:  arrayFormat,
	}

	// Add query parameters:
	if values, err := apiquery.MarshalWithSettings(requestContents.Queries, querySettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithQueryDel(k))
			} else {
				options = append(options, option.WithQuery(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithQueryAdd(k, v))
				}
			}
		}
	}

	// Add header parameters
	headerSettings := apiquery.QuerySettings{
		NestedFormat: apiquery.NestedQueryFormatDots,
		ArrayFormat:  apiquery.ArrayQueryFormatRepeat,
	}
	if values, err := apiquery.MarshalWithSettings(requestContents.Headers, headerSettings); err != nil {
		return nil, err
	} else {
		for k, vs := range values {
			if len(vs) == 0 {
				options = append(options, option.WithHeaderDel(k))
			} else {
				options = append(options, option.WithHeader(k, vs[0]))
				for _, v := range vs[1:] {
					options = append(options, option.WithHeaderAdd(k, v))
				}
			}
		}
	}

	switch bodyType {
	case EmptyBody:
		break
	case MultipartFormEncoded:
		// For multipart/form-encoded, we need a map structure
		bodyMap, ok := requestContents.Body.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("Cannot send a non-map value to a form-encoded endpoint: %v\n", requestContents.Body)
		}
		if err := prepareImageMultipartBody(cmd, bodyMap); err != nil {
			return nil, err
		}
		// Multipart dispatch follows actual Booleans without consuming uploads
		// or interpreting strings that happen to resemble scalar values.
		if streaming, ok := bodyMap["stream"].(bool); ok {
			if err := resolveStreamingValue(cmd, streaming); err != nil {
				return nil, err
			}
		}
		encodingFormat := apiform.FormatBrackets
		// Saving prepares uploads before the generated action takes over. Keep
		// an idempotent closer for failures between preparation and dispatch.
		var observers []func(io.Closer)
		if state, ok := cmd.Metadata[imageMultipartMetadata].(*imageMultipartPreparation); ok {
			observers = append(observers, func(body io.Closer) { state.body = body })
		}
		multipartOptions, err := requestconfig.MultipartRequestOptions(bodyMap, encodingFormat, observers...)
		if err != nil {
			return nil, err
		}
		pendingMultipartUploads = nil // the multipart request body owns them now.
		options = append(options, multipartOptions...)

	case ApplicationJSON:
		bodyBytes, err := json.Marshal(requestContents.Body)
		if err != nil {
			return nil, err
		}
		if err := validateProjectAPIKeyUpdate(cmd, bodyBytes); err != nil {
			return nil, err
		}
		if err := resolveStreamingInput(cmd, bodyBytes); err != nil {
			return nil, err
		}
		options = append(options, option.WithRequestBody("application/json", bodyBytes))
		for _, observe := range observeJSON {
			observe(bodyBytes)
		}

	case ApplicationOctetStream:
		// If there is a body root parameter, that will handle setting the request body, we don't need to do it here.
		for _, flag := range cmd.Flags {
			if toSend, ok := flag.(requestflag.InRequest); ok && toSend.IsBodyRoot() {
				return append(options, headerOptions...), nil
			}
		}
		if bodyBytes, ok := requestContents.Body.([]byte); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", bodyBytes))
		} else if bodyStr, ok := requestContents.Body.(string); ok {
			options = append(options, option.WithRequestBody("application/octet-stream", []byte(bodyStr)))
		} else {
			return nil, fmt.Errorf("Unsupported body for application/octet-stream: %v", requestContents.Body)
		}

	default:
		panic("Invalid body content type!")
	}

	return append(options, headerOptions...), nil
}
