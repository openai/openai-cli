package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// apiErrorValue preserves API error data when available. Proxies and gateways
// can return HTML, plain text, or an empty body; these still need a structured
// diagnostic. The fallback uses only the HTTP status, never response contents,
// request URLs, or credentials.
func apiErrorValue(apierr *openai.Error) gjson.Result {
	raw := apierr.RawJSON()
	if gjson.Valid(raw) {
		value := gjson.Parse(raw)
		if value.Type != gjson.Null {
			return value
		}
	}
	message := fmt.Sprintf("HTTP %d", apierr.StatusCode)
	if reason := http.StatusText(apierr.StatusCode); reason != "" {
		message += " " + reason
	}
	message += ": the server returned no usable JSON error details."
	// Integer and string fields cannot fail JSON encoding.
	data, _ := json.Marshal(struct {
		StatusCode int    `json:"status_code"`
		Message    string `json:"message"`
	}{apierr.StatusCode, message})
	return gjson.ParseBytes(data)
}

// errorOutputFormat resolves the requested error format, inheriting an explicit
// machine-readable output format unless --format-error overrides it.
func errorOutputFormat(root *cli.Command) string {
	format := root.String("format-error")
	if !root.IsSet("format-error") && root.IsSet("format") {
		switch strings.ToLower(root.String("format")) {
		case "json", "jsonl", "raw", "yaml":
			format = root.String("format")
		}
	}
	return resolvedOutputFormat(ShowJSONOpts{Format: format, Transform: root.String("transform-error")})
}

// ShowCommandError presents a failure without changing its identity or exit code.
// Explicit data formats preserve the API payload. Human summaries never echo API
// prose, request values, URLs or arbitrary local diagnostics. Both renderer sinks
// use out, including explorer output and warnings. Writer failures are returned.
func ShowCommandError(root *cli.Command, failure error, out io.Writer) error {
	if failure == nil {
		return nil
	}
	var exit cli.ExitCoder
	if errors.As(failure, &exit) && exit.Error() == "" {
		// Completion uses empty ExitCoder messages for its protocol statuses.
		return nil
	}
	format := errorOutputFormat(root)
	// A rejected format can still be present in the parsed flag state. Present
	// its validation error using text, rather than failing to render it again.
	if !slices.Contains(OutputFormats, format) {
		format = "text"
	}
	var apierr *openai.Error
	isAPI := errors.As(failure, &apierr)
	var streamerr *ssestream.StreamError
	isStream := !errors.Is(failure, context.Canceled) && errors.As(failure, &streamerr)
	if format == "text" && root.String("transform-error") == "" {
		message := ""
		if isAPI && !errors.Is(failure, context.Canceled) {
			message = readableAPIErrorMessage(root, failure, apierr)
		} else if isStream {
			message = readableStreamErrorText
		} else {
			message = localErrorMessage(root, failure)
		}
		return readable.WriteText(out, message)
	}
	var value gjson.Result
	if isAPI {
		value = apiErrorValue(apierr)
	} else if isStream && gjson.ValidBytes(streamerr.Event.Data) {
		// A stream can fail after HTTP 200. Preserve its whole event payload,
		// including the error envelope and unknown fields, without inventing
		// an HTTP failure status. Extraction paths are relative to this event.
		value = gjson.ParseBytes(streamerr.Event.Data)
	}
	if value.Type == gjson.Null {
		// A string-only payload cannot fail encoding. Do not fabricate status
		// codes for decoding or transport failures that discarded the response.
		message := readableStreamErrorMessage
		if !isStream {
			message = localErrorMessage(root, failure)
		}
		data, _ := json.Marshal(struct {
			Message string `json:"message"`
		}{message})
		value = gjson.ParseBytes(data)
	}
	return ShowJSON(value, ShowJSONOpts{
		Context:   outputPolicyContext(context.Background(), root),
		Operation: "", OutputKind: OutputUnspecified,
		Format: format, ExplicitFormat: root.IsSet("format-error") || root.IsSet("format"),
		Stdout: out, Stderr: out, Title: "Error", Transform: root.String("transform-error"),
	})
}

// Keep the existing structured fallback payload independent of human guidance.
const readableStreamErrorMessage = "The response stream failed. Output may be incomplete.\nFor API error details, add --format-error json."
const readableStreamErrorText = "The response stream failed.\nOutput may be incomplete.\nAPI error details: --format-error json."

func readableAPIErrorMessage(root *cli.Command, failure error, apierr *openai.Error) string {
	invocation := errorHelpInvocation(root)
	message := "The API could not complete the request."
	switch apierr.StatusCode {
	case http.StatusUnauthorized:
		message = "Check API key, organization and project.\nKey setup: " + invocation + " help setup"
	case http.StatusForbidden:
		message = "Check your key's permissions.\nCheck project access to this resource."
	case http.StatusNotFound:
		message = "Check the resource or model ID.\nCheck your key's access to it."
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		var contextual *commandError
		var command *cli.Command
		if errors.As(failure, &contextual) {
			command = contextual.command
		}
		message = readableAPIArgumentMessage(apierr, command)
	case http.StatusTooManyRequests:
		message = "Wait before trying again."
		switch apierr.Code {
		case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded":
			message = "API usage or billing limit reached.\nCheck your project's billing and limits."
		}
	case http.StatusConflict:
		message = "The resource changed or is not ready.\nCheck its current status."
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		message = "The request timed out.\nThe API may have received the request.\nCheck its status before trying again."
	default:
		if apierr.StatusCode >= 500 {
			message = "The API is temporarily unavailable.\nThe API may have received the request.\nCheck its status before trying again."
		}
	}
	if reportMessage := costReportAPIErrorMessage(failure, apierr); reportMessage != "" {
		message = reportMessage
	}
	if apierr.StatusCode > 0 {
		status := fmt.Sprintf("HTTP %d", apierr.StatusCode)
		if reason := http.StatusText(apierr.StatusCode); reason != "" {
			status += ": " + reason
		}
		header := status + ".\n"
		if requestID := readableAPIRequestID(apierr); requestID != "" {
			header += "Request ID: " + requestID + "\n"
		}
		message = header + message
	}
	return message + "\nAPI error details: --format-error json."
}

// Accept only bounded, single-line returned identifiers.
func readableAPIRequestID(apierr *openai.Error) string {
	if apierr == nil {
		return ""
	}
	// A caller-supplied header can be reflected by the server. Inspect keys only.
	if apierr.Request != nil {
		for name := range apierr.Request.Header {
			if strings.EqualFold(name, "X-Request-ID") {
				return ""
			}
		}
	}
	if apierr.Response == nil {
		return ""
	}
	requestID := apierr.Response.Header.Get("X-Request-ID")
	if len(requestID) == 0 || len(requestID) > 256 {
		return ""
	}
	for _, c := range requestID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-') {
			return ""
		}
	}
	return requestID
}

// Help's invocation is local display metadata, not part of the API response.
func errorHelpInvocation(root *cli.Command) string {
	invocation, _ := root.Metadata["help-invocation"].(string)
	if invocation == "" {
		return "openai"
	}
	return readable.Text(invocation)
}

var readableParameterPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(?:(?:\.[A-Za-z0-9_]+)|(?:\[[0-9]+\]))*$`)
var readableParameterIndex = regexp.MustCompile(`\[[0-9]+\]`)

// Parameter names are untrusted API data. Match only paths declared by this
// exact command, and print only the matching locally defined flag name. Never
// display an arbitrary param, server message, rejected value, or remote choice.
func readableAPIArgumentMessage(apierr *openai.Error, command *cli.Command) string {
	flagName, path := readableParameterFlag(command, apierr.Param)
	message := "The API rejected the request."
	if flagName != "" {
		message = "The API rejected " + flagName + "."
	}
	switch apierr.Code {
	case "missing_required_parameter", "missing_required_argument":
		if flagName != "" {
			message = "Add " + flagName + " with a value."
		} else {
			message = "A required value is missing from the request."
		}
	case "unknown_parameter", "unsupported_parameter":
		if flagName != "" {
			message = "This request does not support " + flagName + ". Remove it or choose a model that supports it."
		} else {
			message = "This request contains an unsupported parameter. Check the model and command's supported options."
		}
	case "context_length_exceeded":
		message = "The request is too long for this model. Shorten the input or conversation, or choose a model with a larger context window."
	case "model_not_found":
		message = "That model is unavailable or your project cannot access it. Check the model and project access."
		if declaredErrorFlag(command, "model") != "" {
			message = "That model is unavailable or your project cannot access it. Check --model and project access."
		}
	case "content_policy_violation":
		message = "The API declined this request under its content policy. Review the input before trying again."
	case "invalid_type":
		message += " Check the value's type (for example, a number, text, or JSON object)."
	case "string_above_max_length", "array_above_max_length", "too_many_items":
		message += " Use a shorter value or fewer items."
	case "invalid_value", "invalid_enum_value", "unsupported_value", "value_error":
		message += " Choose a supported value."
	}
	if choices := readableParameterChoices(command, path); choices != "" && flagName != "" && apierr.Code != "unsupported_parameter" && apierr.Code != "unknown_parameter" {
		message += "\n" + flagName + ": " + choices
	}
	if command == nil {
		return message + "\nCheck your arguments with --help."
	}
	invocation := errorHelpInvocation(command.Root())
	pathToCommand := strings.TrimPrefix(command.FullName(), command.Root().Name+" ")
	return message + "\nOptions and examples: " + invocation + " help " + pathToCommand
}

func readableParameterFlag(command *cli.Command, parameter string) (name, path string) {
	if command == nil || !readableParameterPattern.MatchString(parameter) {
		return "", ""
	}
	// Normalizing array positions makes messages[0].content and
	// messages.0.content resolve to the same declared request path.
	parts := strings.Split(readableParameterIndex.ReplaceAllString(parameter, ""), ".")
	named := parts[:0]
	for _, part := range parts {
		if strings.Trim(part, "0123456789") != "" {
			named = append(named, part)
		}
	}
	parameter = strings.Join(named, ".")
	for _, flag := range command.Flags {
		for _, candidate := range readableFlagPaths(flag) {
			if candidate == "" || len(candidate) <= len(path) || parameter != candidate && !strings.HasPrefix(parameter, candidate+".") {
				continue
			}
			if len(flag.Names()) == 0 {
				continue
			}
			name, path = "--"+flag.Names()[0], candidate
			if len(flag.Names()[0]) == 1 {
				name = "-" + flag.Names()[0]
			}
		}
	}
	return name, path
}

func readableFlagPaths(flag cli.Flag) []string {
	if inner, ok := flag.(requestflag.HasOuterFlag); ok {
		paths := readableFlagPaths(inner.GetOuterFlag())
		for i := range paths {
			if paths[i] != "" {
				paths[i] += "." + inner.GetInnerField()
			}
		}
		return paths
	}
	if parameter, ok := flag.(requestflag.InRequest); ok {
		return []string{parameter.GetBodyPath(), parameter.GetQueryPath(), parameter.GetPathParam()}
	}
	return nil
}

func readableParameterChoices(command *cli.Command, path string) string {
	if command == nil || len(command.Lineage()) < 2 {
		return ""
	}
	// Keep choices scoped to API resources whose contract declares them. A
	// different model can impose additional restrictions, so these are guidance,
	// not a new client-side validation policy.
	switch commandResourceName(command) {
	case "embeddings":
		if path == "encoding_format" {
			return "float or base64."
		}
	case "audio:transcriptions":
		if path == "response_format" {
			return "json, text, srt, verbose_json, vtt, or diarized_json; model support varies."
		}
	case "audio:translations":
		if path == "response_format" {
			return "json, text, srt, verbose_json, or vtt; model support varies."
		}
	}
	return ""
}
