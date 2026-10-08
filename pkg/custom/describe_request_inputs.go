package custom

import (
	"strings"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

// Annotate the generated handlers before handwritten commands and aliases are
// installed. Generated handlers consume URL path inputs in declaration order;
// a handwritten flag's URL location alone does not establish positional syntax.
func describeRequestInputs(root *cli.Command) {
	for _, resource := range root.Commands {
		if resource.Category != "API RESOURCE" {
			continue
		}
		for _, command := range resource.Commands {
			if command.Action == nil {
				continue
			}
			var positional []string
			var descriptions []clihelp.FlagUsage
			for _, flag := range command.Flags {
				path, ok := flag.(interface{ GetPathParam() string })
				if !ok || path.GetPathParam() == "" || len(flag.Names()) == 0 {
					continue
				}
				name := flag.Names()[0]
				positional = append(positional, name)
				if doc, ok := flag.(cli.DocGenerationFlag); ok && strings.TrimSpace(doc.GetUsage()) == "" {
					descriptions = append(descriptions, clihelp.FlagUsage{
						Owner: command, Names: []string{name}, Usage: pathInputDescription(path.GetPathParam()),
					})
				}
			}
			if len(positional) == 0 {
				continue
			}
			if command.Metadata == nil {
				command.Metadata = map[string]any{}
			}
			command.Metadata["help-positional-flags"] = positional
			command.Metadata["help-flag-usages"] = descriptions
		}
	}
}

func pathInputDescription(parameter string) string {
	switch parameter {
	case "id":
		return "Resource ID."
	case "model":
		return "Model ID. Use models list to find available model IDs."
	case "version":
		return "Version identifier."
	default:
		return "ID of the " + strings.ReplaceAll(strings.TrimSuffix(parameter, "_id"), "_", " ") + "."
	}
}
