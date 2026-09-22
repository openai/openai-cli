package custom

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// APIErrorValue preserves API error data when available. Proxies and gateways
// can return HTML, plain text, or an empty body; these still need a structured
// diagnostic. The fallback uses only the HTTP status, never response contents,
// request URLs, or credentials.
func APIErrorValue(apierr *openai.Error) gjson.Result {
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

// ErrorOutputFormat resolves the requested error format, inheriting an explicit
// machine-readable output format unless --format-error overrides it.
func ErrorOutputFormat(root *cli.Command) string {
	format := root.String("format-error")
	if !root.IsSet("format-error") && root.IsSet("format") {
		switch strings.ToLower(root.String("format")) {
		case "json", "jsonl", "raw", "yaml":
			format = root.String("format")
		}
	}
	return resolvedOutputFormat(ShowJSONOpts{Format: format, Transform: root.String("transform-error")})
}

// ShowReadableError writes a concise error summary and reports whether it handled
// the error. Summaries use status and known codes, never server-supplied prose that
// may contain a rejected key, prompt, signed URL, or terminal control sequence.
// Detailed API data remains available through an explicit output format.
func ShowReadableError(root *cli.Command, failure error, out io.Writer) bool {
	if ErrorOutputFormat(root) != "text" || root.String("transform-error") != "" {
		return false
	}
	if errors.Is(failure, context.Canceled) {
		fmt.Fprintln(out, "Request canceled.")
		return true
	}
	invocation, _ := root.Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	var apierr *openai.Error
	if errors.As(failure, &apierr) {
		message := "The API could not complete the request."
		switch apierr.StatusCode {
		case http.StatusUnauthorized:
			message = "Authentication failed. Check your API key, organization, and project.\nKey setup: " + invocation + " help setup"
		case http.StatusForbidden:
			message = "Access denied. Check the key's permissions and your project's access to this resource."
		case http.StatusNotFound:
			message = "The requested resource or model was not found or is not available to your key."
		case http.StatusBadRequest, http.StatusUnprocessableEntity:
			var commandError *readableCommandError
			var command *cli.Command
			if errors.As(failure, &commandError) {
				command = commandError.command
			}
			message = readableAPIArgumentMessage(apierr, command)
		case http.StatusTooManyRequests:
			message = "Rate limit reached. Wait before trying again."
			switch apierr.Code {
			case "insufficient_quota", "billing_hard_limit_reached", "credit_balance_exhausted", "organization_spend_limit_exceeded", "project_spend_limit_exceeded", "organization_usage_limit_exceeded":
				message = "API usage or billing limit reached. Check your project's billing and limits."
			}
		case http.StatusConflict:
			message = "The resource changed or is not ready for this operation. Check its current status."
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			message = "The request timed out. The API may have received it; check its status before repeating it."
		default:
			if apierr.StatusCode >= 500 {
				message = "The API is temporarily unavailable. The request may have been received; check its status before repeating it."
			}
		}
		fmt.Fprintf(out, "Request failed (%d %s).\n%s\nFor API error details, add --format-error json.\n", apierr.StatusCode, http.StatusText(apierr.StatusCode), message)
		return true
	}
	var networkError *url.Error
	if errors.As(failure, &networkError) {
		var timeout net.Error
		if errors.Is(failure, context.DeadlineExceeded) || errors.As(failure, &timeout) && timeout.Timeout() {
			fmt.Fprintln(out, "The request timed out. The API may have received it; check its status before repeating it.")
		} else {
			fmt.Fprintln(out, "Could not connect to the API. Check your connection, proxy, and --base-url setting.")
		}
		return true
	}
	return false
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
		message = "That model is unavailable or your project cannot access it. Check --model and project access."
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
	invocation, _ := command.Root().Metadata["help-invocation"].(string)
	if invocation == "" {
		invocation = "openai"
	}
	pathToCommand := strings.TrimPrefix(command.FullName(), command.Root().Name+" ")
	return message + "\nOptions and examples: " + invocation + " help --all " + pathToCommand
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
	switch command.Lineage()[1].Name {
	case "images":
		switch path {
		case "output_format":
			return "png, jpeg, or webp (model support varies)."
		case "background":
			return "auto, transparent, or opaque; transparency needs png or webp."
		case "moderation":
			return "auto or low (model support varies)."
		case "quality":
			return "auto, low, medium, high, xhigh, or max; model support varies."
		case "partial_images":
			return "0 (none), 1, 2, or 3 progress previews."
		case "n":
			return "a whole number from 1 to 10; some models support only 1."
		}
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
