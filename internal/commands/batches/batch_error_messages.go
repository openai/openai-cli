package batches

import "strings"

// WorkflowErrorMessage retains safe outcome and cleanup messages from joined
// failures. Arbitrary cause text remains available through explicit error modes.
func WorkflowErrorMessage(failure error) string {
	var messages []string
	var visit func(error)
	visit = func(err error) {
		if err == nil {
			return
		}
		if workflow, ok := err.(*WorkflowError); ok && workflow.message != "" {
			messages = append(messages, workflow.message)
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
	var distinct []string
	for i, message := range messages {
		contained := false
		for j, other := range messages {
			// Prefer complete guidance regardless of cleanup-join order.
			// Keep only the first occurrence of identical messages.
			if i != j && strings.Contains(other, message) && (other != message || j < i) {
				contained = true
				break
			}
		}
		if !contained {
			distinct = append(distinct, message)
		}
	}
	return strings.Join(distinct, "\n")
}
