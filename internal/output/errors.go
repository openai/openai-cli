package output

import "strings"

// DiagnosticError identifies failures that belong to stderr, even inside stdout's stream lifecycle.
type DiagnosticError struct{ Err error }

func (e *DiagnosticError) Error() string { return e.Err.Error() }
func (e *DiagnosticError) Unwrap() error { return e.Err }

// WriteError identifies failures from the output sink, rather than from
// the iterator, transport, or formatter producing the output.
type WriteError struct{ Err error }

func (e *WriteError) Error() string { return e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// IsBrokenPipe reports whether every cause is a broken output pipe, excluding
// diagnostic writes and upstream failures.
func IsBrokenPipe(err error) bool {
	if _, diagnostic := err.(*DiagnosticError); diagnostic {
		return false
	}
	if outputErr, ok := err.(*WriteError); ok {
		return strings.Contains(outputErr.Error(), "broken pipe")
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !IsBrokenPipe(cause) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return IsBrokenPipe(wrapped.Unwrap())
	}
	return false
}
