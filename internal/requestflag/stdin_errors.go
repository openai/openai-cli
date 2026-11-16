package requestflag

import "github.com/urfave/cli/v3"

// StdinValueError identifies the declaration that rejected piped input.
// Error and Unwrap preserve the existing diagnostic and its original causes.
type StdinValueError struct {
	name  string
	cause error
}

func (e *StdinValueError) Error() string    { return e.cause.Error() }
func (e *StdinValueError) Unwrap() error    { return e.cause }
func (e *StdinValueError) FlagName() string { return e.name }

func stdinValueError(flag cli.Flag, cause error) error {
	return &StdinValueError{name: flag.Names()[0], cause: cause}
}
