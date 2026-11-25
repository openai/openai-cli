package clihelp

// MissingInputError marks a local usage error for automatic command help.
// Cause keeps the original structured diagnostic and exit status. Message can
// name the missing input more precisely in text output.
type MissingInputError struct {
	Cause   error
	Message string
}

func (e *MissingInputError) Error() string { return e.Cause.Error() }
func (e *MissingInputError) Unwrap() error { return e.Cause }

// MissingInput marks an existing diagnostic for a required input that was omitted.
func MissingInput(err error) error { return &MissingInputError{Cause: err} }
