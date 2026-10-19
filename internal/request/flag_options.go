package request

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	"github.com/openai/openai-cli/internal/debugmiddleware"
	cliinput "github.com/openai/openai-cli/internal/input"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// PreparationHooks keeps feature validation and multipart specialization with
// assembly while shared request code owns input precedence and upload cleanup.
type PreparationHooks struct {
	PreflightMultipart  func(map[string]any) error
	ValidateCredentials func() error
	// EmbedMultipart owns partial-result cleanup on failure. On success, shared
	// preparation takes responsibility for the returned uploads until encoding.
	EmbedMultipart func(map[string]any, *OnceStdinReader) (map[string]any, error)
	Encoding       EncodingHooks
}

// FlagOptions prepares request options from flags and borrowed stdin. Stdin
// classification retains the process stdin probe even when a workflow supplies
// a cancellation-aware reader. ignoreStdin reserves stdin for a binary body.
// Feature callbacks run at the same preflight, credential, expansion, and encoding
// points as ordinary request preparation, before upload ownership transfers.
func FlagOptions(
	cmd *cli.Command,
	nestedFormat apiquery.NestedQueryFormat,
	arrayFormat apiquery.ArrayQueryFormat,
	bodyType BodyContentType,
	ignoreStdin bool,
	stdin io.Reader,
	hooks PreparationHooks,
) (options []option.RequestOption, err error) {
	// Validate literal headers before reading any request input. Flag validators
	// include the supplied value in errors, which can expose credentials.
	headers, err := Headers(cmd)
	if err != nil {
		return nil, err
	}
	headerOptions := make([]option.RequestOption, 0, len(headers))
	for name, values := range headers {
		headerOptions = append(headerOptions, option.WithHeader(name, values[0]))
	}

	stdinSecurity, err := NewStdinSecurity()
	if err != nil {
		return nil, err
	}

	// Once multipart files have been opened, close them on every error path
	// until ownership is transferred to the multipart request body below.
	var pendingMultipartUploads any
	defer func() {
		if err != nil && pendingMultipartUploads != nil {
			err = errors.Join(err, CloseFileUploads(pendingMultipartUploads))
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
		ApplyDataAliases(cmd, bodyMap)
	}
	// Resolve explicit consumers before attempting automatic whole-request input.
	// FileInput values must retain their literal path semantics during inspection.
	if err := WrapFileInputValues(cmd, &requestContents, stdinSecurity); err != nil {
		return nil, err
	}
	explicitStdin, err := StdinConsumers(requestContents, ignoreStdin)
	if err != nil {
		return nil, err
	}

	stdinConsumedByPipe := false
	if bodyType != ApplicationOctetStream && !ignoreStdin && explicitStdin == 0 && cliinput.IsPiped(os.Stdin) {
		stdinConsumedByPipe, err = ReadPipedInput(cmd, stdin, &requestContents, stdinSecurity, bodyType != EmptyBody)
		if err != nil {
			return nil, err
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
			if hooks.PreflightMultipart != nil {
				if err := hooks.PreflightMultipart(bodyMap); err != nil {
					return nil, err
				}
			}
		}
	}

	if hooks.ValidateCredentials != nil {
		if err := hooks.ValidateCredentials(); err != nil {
			return nil, err
		}
	}

	// FileInput values are file paths, so wrap trusted values with FilePathValue
	// for automatic expansion. In untrusted-stdin mode, reject piped values.
	if err := WrapFileInputValues(cmd, &requestContents, stdinSecurity); err != nil {
		return nil, err
	}
	if _, err := StdinConsumers(requestContents, ignoreStdin); err != nil {
		return nil, err
	}

	// Determine stdin availability for FileInput params that use "-".
	var stdinReader OnceStdinReader
	if ignoreStdin {
		stdinReader = OnceStdinReader{FailureReason: "stdin is already being used for the request body"}
	} else if stdinConsumedByPipe {
		stdinReader = OnceStdinReader{FailureReason: "stdin was already consumed by piped YAML/JSON input"}
	} else {
		stdinReader = OnceStdinReader{Reader: stdin}
	}

	// Embed files passed as "@file.jpg" in the request body, headers, and query:
	embedStyle := EmbedText
	if bodyType == ApplicationOctetStream || bodyType == MultipartFormEncoded {
		embedStyle = EmbedIOReader
	}

	var embedded any
	bodyMap, bodyIsMap := requestContents.Body.(map[string]any)
	if bodyType == MultipartFormEncoded && bodyIsMap && hooks.EmbedMultipart != nil {
		embedded, err = hooks.EmbedMultipart(bodyMap, &stdinReader)
	} else {
		embedded, err = EmbedRequestFiles(cmd, requestContents.Body, "body", embedStyle, &stdinReader)
	}
	if err != nil {
		return nil, err
	}
	requestContents.Body = embedded
	if bodyType == MultipartFormEncoded {
		pendingMultipartUploads = embedded
	}

	if headersWithFiles, err := EmbedRequestFiles(cmd, requestContents.Headers, "header", EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Headers = headersWithFiles.(map[string]any)
	}
	if queriesWithFiles, err := EmbedRequestFiles(cmd, requestContents.Queries, "query", EmbedText, &stdinReader); err != nil {
		return nil, err
	} else {
		requestContents.Queries = queriesWithFiles.(map[string]any)
	}

	encodedOptions, err := EncodeOptions(cmd, requestContents, nestedFormat, arrayFormat, bodyType, hooks.Encoding)
	if err != nil {
		return nil, err
	}
	if bodyType == MultipartFormEncoded {
		pendingMultipartUploads = nil // the multipart request body owns them now.
	}
	options = append(options, encodedOptions...)

	return append(options, headerOptions...), nil
}
