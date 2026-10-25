package batches

import "strings"

// WorkflowErrorMessage retains safe outcome and cleanup messages from joined
// failures. Arbitrary cause text remains available through explicit error modes.
func WorkflowErrorMessage(failure error) string {
	var message strings.Builder
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if workflow, ok := err.(*WorkflowError); ok && workflow.message != "" && !strings.Contains(message.String(), workflow.message) {
			if message.Len() > 0 {
				message.WriteByte('\n')
			}
			message.WriteString(workflow.message)
		}
		switch wrapped := err.(type) {
		case interface{ Unwrap() []error }:
			for _, cause := range wrapped.Unwrap() {
				visit(cause)
			}
		case interface{ Unwrap() error }:
			visit(wrapped.Unwrap())
		}
	}
	visit(failure)
	return message.String()
}
