package custom

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureHelp decorates help without changing API commands or their defaults.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	configureHelpGroups(root)
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

func configureHelpGroups(root *cli.Command) {
	groups := []clihelp.FlagGroup{
		{Title: "Response output", Names: []string{"format", "format-error", "transform", "transform-error", "raw-output"}},
		{Title: "Request configuration", Names: []string{"api-key", "admin-api-key", "webhook-secret", "organization", "project", "base-url", "header", "mtls-client-cert-file", "mtls-client-key-file", "debug"}},
	}
	var visit func(*cli.Command, bool)
	visit = func(command *cli.Command, image bool) {
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		sections := append([]clihelp.FlagGroup(nil), groups...)
		if image {
			sections = append([]clihelp.FlagGroup{
				{Title: "Image settings", Names: []string{"model", "n", "size", "quality", "background", "output-format", "output-compression", "moderation", "input-fidelity", "mask", "user"}},
				{Title: "Saving and previews", Names: []string{"output-dir", "name", "inline", "stream", "partial-images", "response-format"}},
			}, sections...)
		}
		command.Metadata["help-flag-groups"] = sections
		for _, child := range command.Commands {
			visit(child, image || child == root.Command("images"))
		}
	}
	visit(root, false)
}
