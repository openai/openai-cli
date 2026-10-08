package custom

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// Share this policy with optional output hints and command diagnostics.
// A selected machine error stream must not receive a successful-save receipt.
func commandAllowsOutputDiagnostics(command *cli.Command) bool {
	root := command.Root()
	return !root.Bool("quiet") && errorOutputFormat(root) == "text" && root.String("transform-error") == ""
}

type saveReceiptError struct{ cause error }

func (e *saveReceiptError) Error() string {
	return "The output was saved, but its receipt could not be written."
}
func (e *saveReceiptError) Unwrap() error { return e.cause }

type saveReceiptPolicyKey struct{}

type saveReceiptPolicy struct {
	enabled bool
	stderr  io.Writer
}

// Snapshot command policy without retaining mutable command state. Middleware
// carries it through requests; only completed saves emit a receipt.
func captureSaveReceiptPolicy(command *cli.Command) func(*http.Request, func(*http.Request) (*http.Response, error)) (*http.Response, error) {
	policy := saveReceiptPolicy{enabled: commandAllowsOutputDiagnostics(command), stderr: os.Stderr}
	return func(request *http.Request, next func(*http.Request) (*http.Response, error)) (*http.Response, error) {
		request = request.WithContext(context.WithValue(request.Context(), saveReceiptPolicyKey{}, policy))
		response, err := next(request)
		if response == nil {
			return nil, err
		}
		// Preserve the final request's cancellation and endpoint context, including
		// redirects. Custom transports can omit Response.Request entirely.
		copyResponse := *response
		finalRequest := response.Request
		if finalRequest == nil {
			finalRequest = request
		}
		copyResponse.Request = finalRequest.WithContext(context.WithValue(finalRequest.Context(), saveReceiptPolicyKey{}, policy))
		return &copyResponse, err
	}
}

func reportResponseSaveReceipt(response *http.Response, message string, operationErr error) (string, error) {
	if response.Request == nil {
		return message, operationErr
	}
	policy, ok := response.Request.Context().Value(saveReceiptPolicyKey{}).(saveReceiptPolicy)
	if !ok {
		return message, operationErr
	}
	// Consuming the message also suppresses the generated caller's legacy print.
	// A future generated receipt adapter receives an empty message and stays quiet.
	return "", policy.report(message, operationErr)
}

// ReportSaveReceipt keeps successful save receipts separate from response bytes.
// Callers use this after completing their work; binary responses use the same policy.
func ReportSaveReceipt(command *cli.Command, stderr io.Writer, message string, operationErr error) error {
	return (saveReceiptPolicy{enabled: commandAllowsOutputDiagnostics(command), stderr: stderr}).report(message, operationErr)
}

func (policy saveReceiptPolicy) report(message string, operationErr error) error {
	if operationErr != nil || message == "" || !policy.enabled {
		return operationErr
	}
	if err := readable.WriteText(policy.stderr, message); err != nil {
		return errors.Join(operationErr, &saveReceiptError{cause: err})
	}
	return nil
}
