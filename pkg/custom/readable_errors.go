package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/urfave/cli/v3"
)

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

// Error summaries use status and known codes, never server-supplied prose that
// may contain a rejected key, prompt, signed URL, or terminal control sequence.
// Detailed API data remains available through an explicit output format.
func showReadableError(root *cli.Command, failure error, out io.Writer) bool {
	if errorOutputFormat(root) != "text" || root.String("transform-error") != "" {
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
			message = "The API rejected the request. Check your arguments with --help."
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
