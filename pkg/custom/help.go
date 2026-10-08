package custom

import (
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureHelp decorates help without changing API commands or their defaults.
func ConfigureHelp(root *cli.Command, args []string) ([]string, bool, error) {
	orderImageCommands(root)
	configureHelpGroups(root)
	configureImageHelpInvocation(root, clihelp.Invocation(root.Name, args))
	// Full help documents credential flags, but must never echo their values.
	for _, flag := range root.Flags {
		if flag, ok := flag.(*requestflag.Flag[string]); ok {
			switch flag.Name {
			case "api-key", "admin-api-key", "webhook-secret":
				flag.HideDefault = true
			}
		}
	}
	args, handled, err := clihelp.Configure(root, args)
	configureAdminSetupHelp(root)
	return args, handled, err
}

func configureImageHelpInvocation(root *cli.Command, invocation string) {
	images := root.Command("images")
	if images == nil {
		return
	}
	for name, description := range map[string]string{
		"generate":         imageGenerationSavingHelp,
		"edit":             imageEditSavingHelp,
		"create-variation": imageVariationSavingHelp,
	} {
		if command := images.Command(name); command != nil {
			// Descriptions are plain text, not templates. Start from the original
			// each time so repeated help configuration cannot retain an old path.
			command.Description = strings.Replace(description, "\n    openai ", "\n    "+invocation+" ", 1)
		}
	}
}

func configureHelpGroups(root *cli.Command) {
	groups := []clihelp.FlagGroup{
		{Title: "Response output", Names: []string{"format", "format-error", "transform", "transform-error", "raw-output"}, Owner: root},
		{Title: "Request configuration", Names: []string{"api-key", "admin-api-key", "webhook-secret", "organization", "project", "base-url", "header", "mtls-client-cert-file", "mtls-client-key-file", "debug"}, Owner: root},
	}
	var visit func(*cli.Command, bool)
	visit = func(command *cli.Command, image bool) {
		if command.Metadata == nil {
			command.Metadata = map[string]any{}
		}
		sections := append([]clihelp.FlagGroup(nil), groups...)
		if image {
			sections = append([]clihelp.FlagGroup{
				{Title: "Image settings", Names: []string{"model", "n", "size", "quality", "background", "moderation", "input-fidelity", "mask"}},
				{Title: "Image file format", Names: []string{"output-format", "output-compression"}},
				{Title: "Saving", Names: []string{"output-dir", "name"}},
				{Title: "Terminal previews", Names: []string{"inline"}},
				{Title: "Progress", Names: []string{"stream", "partial-images", "max-items"}},
				{Title: "API response", Names: []string{"response-format"}},
			}, sections...)
		}
		command.Metadata["help-flag-groups"] = sections
		for _, child := range command.Commands {
			visit(child, image || child == root.Command("images"))
		}
	}
	visit(root, false)
}
