package custom

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

// ConfigureHelp attaches feature-specific help, then delegates local guide
// routing to clihelp. The entrypoint retains ownership of running the command.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	configureImageHelp(root, clihelp.Invocation(root.Name, args))
	return clihelp.Configure(root, args)
}
