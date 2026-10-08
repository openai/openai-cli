package custom

import "github.com/urfave/cli/v3"

const localUtilityMetadata = "openai-local-utility"

const localUtilityGlobalHelp = `
{{$bin := or (index .Root.Metadata "help-invocation") "openai"}}Global option details: {{$bin}} --help
`

// IsLocalUtilityCommand identifies a registered utility after root flag parsing.
// Request values and unregistered command names never select the local path.
func IsLocalUtilityCommand(command *cli.Command) bool {
	root := command.Root()
	selected := root.Command(root.Args().First())
	return selected != nil && selected.Metadata[localUtilityMetadata] == true
}

// Only locally defined messages belong here. Causes retain failure identity
// without exposing input text, paths, configuration values, or launcher output.
type localUtilityError struct {
	message string
	cause   error
}

func (e *localUtilityError) Error() string { return e.message }
func (e *localUtilityError) Unwrap() error { return e.cause }
