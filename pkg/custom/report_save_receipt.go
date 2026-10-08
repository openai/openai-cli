package custom

import (
	"errors"
	"io"

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

// ReportSaveReceipt keeps successful save receipts separate from response bytes.
// Generated binary and local-save commands call this after completing their work.
func ReportSaveReceipt(command *cli.Command, stderr io.Writer, message string, operationErr error) error {
	if operationErr != nil || message == "" || !commandAllowsOutputDiagnostics(command) {
		return operationErr
	}
	if err := readable.WriteText(stderr, message); err != nil {
		return errors.Join(operationErr, &saveReceiptError{cause: err})
	}
	return nil
}
