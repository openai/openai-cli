// Package commands shapes the CLI command tree and defines shared contracts for
// handwritten features. Application assembly owns configuration order.
package commands

// LocalUtilityMetadata marks registered commands that bypass remote setup.
const LocalUtilityMetadata = "openai-local-utility"

// LocalUtilityGlobalHelp points local-command help back to global options.
const LocalUtilityGlobalHelp = `
{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Global option details: {{$bin}} --help
`

// LocalError exposes only authored guidance. Message must not contain rejected
// values, paths, configuration, or launcher diagnostics. Cause preserves failure
// identity without adding its text to the displayed message.
type LocalError struct {
	Message string
	Cause   error
}

func (e *LocalError) Error() string { return e.Message }
func (e *LocalError) Unwrap() error { return e.Cause }
