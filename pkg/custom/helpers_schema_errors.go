package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Preserve both the operation identity and actionable cleanup outcome.
type schemaHelperCleanupError struct{ operation, cleanup error }

func (e *schemaHelperCleanupError) Error() string   { return e.cleanup.Error() }
func (e *schemaHelperCleanupError) Unwrap() []error { return []error{e.operation, e.cleanup} }
func (e *schemaHelperCleanupError) ExitCode() int {
	return (&schemaHelperError{cause: errors.Join(e.operation, e.cleanup)}).ExitCode()
}

func showSchemaCleanupError(root *cli.Command, failure error, out io.Writer, format string) (bool, error) {
	var cleanup *schemaHelperCleanupError
	if !errors.As(failure, &cleanup) {
		return false, nil
	}
	// Explicit extraction and raw errors retain the original API payload contract.
	if root.String("transform-error") != "" || format == "raw" || root.Bool("raw-output") {
		return false, nil
	}
	message := ""
	var apierr *openai.Error
	isAPI := errors.As(cleanup.operation, &apierr)
	if cleanup.operation != nil {
		message = localErrorMessage(root, cleanup.operation)
		if isAPI && !errors.Is(failure, context.Canceled) && !errors.Is(failure, context.DeadlineExceeded) {
			message = readableAPIErrorMessage(root, cleanup.operation, apierr)
		} else if errors.Is(failure, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) {
			message = localErrorMessage(root, failure)
		}
	}
	if format == "text" && root.String("transform-error") == "" {
		if message != "" {
			message += "\n"
		}
		return true, readable.WriteText(out, message+cleanup.cleanup.Error())
	}
	var operation any
	if isAPI {
		operation = json.RawMessage(apiErrorValue(apierr).Raw)
	} else if message != "" {
		operation = map[string]string{"message": message}
	}
	key := "operation_error"
	if isAPI {
		key = "api_error"
	}
	data, err := json.Marshal(map[string]any{key: operation, "cleanup_error": cleanup.cleanup.Error()})
	if err != nil {
		return true, err
	}
	return true, ShowJSON(gjson.ParseBytes(data), ShowJSONOpts{Format: format, Transform: root.String("transform-error"), Stdout: out, Stderr: out})
}

func schemaSavedFailureMessage(failure error) string {
	var schema *schemaHelperError
	if !errors.As(failure, &schema) || !schema.saved {
		return ""
	}
	switch {
	case errors.Is(failure, context.Canceled):
		return "Schema saved, but the command was canceled. Check --output before repeating generation."
	case errors.Is(failure, context.DeadlineExceeded):
		return "Schema saved, but the command timed out. Check --output before repeating generation."
	default:
		return schema.message
	}
}
