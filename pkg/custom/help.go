package custom

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureHelp decorates help without changing API commands or their defaults.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	// Full help documents credential flags, but must never echo their values.
	for _, flag := range root.Flags {
		if flag, ok := flag.(*requestflag.Flag[string]); ok {
			switch flag.Name {
			case "api-key", "admin-api-key", "webhook-secret":
				flag.HideDefault = true
			}
		}
	}
	return clihelp.Configure(root, args)
}
