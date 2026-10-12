package custom

import (
	"maps"
	"slices"
	"strings"

	"github.com/urfave/cli/v3"
)

const resourceCommandMetadata = "openai-resource-command"

// configureCommandSubgroups adds real command groups from the generated resource
// names. Run after feature decoration and before cli.Run: command nodes acquire
// mutable parent/parser state when the framework initializes them.
func configureCommandSubgroups(root *cli.Command) {
	resources := slices.Clone(root.Commands)
	// Install parents before descendants, including when generation changes order.
	slices.SortStableFunc(resources, func(a, b *cli.Command) int {
		return strings.Count(a.Name, ":") - strings.Count(b.Name, ":")
	})
	for _, resource := range resources {
		if resource.Category != "API RESOURCE" || !strings.Contains(resource.Name, ":") {
			continue
		}
		parts := strings.Split(resource.Name, ":")
		parent := root
		for _, part := range parts[:len(parts)-1] {
			child := parent.Command(part)
			if child == nil {
				child = &cli.Command{Name: part, Category: "API RESOURCE", Suggest: true, HideHelpCommand: true}
				parent.Commands = append(parent.Commands, child)
			}
			if child.Category != "API RESOURCE" {
				// Never replace an existing action or handwritten command. Keep
				// the original route visible if future generation creates a clash.
				parent = nil
				break
			}
			parent = child
		}
		if parent == nil || parent.Command(parts[len(parts)-1]) != nil {
			continue
		}
		nested := cloneResourceCommand(resource)
		nested.Name = parts[len(parts)-1]
		nested.Metadata[resourceCommandMetadata] = resource.Name
		parent.Commands = append(parent.Commands, nested)
		if resource.Metadata == nil {
			resource.Metadata = map[string]any{}
		}
		resource.Metadata["command-compatibility-alias"] = true
		resource.Hidden = true
	}
}

// Keep separate nodes and metadata for the canonical and compatibility paths.
// Flags and actions retain their generated definitions; only the selected route
// is parsed during a CLI invocation.
func cloneResourceCommand(source *cli.Command) *cli.Command {
	copy := *source
	copy.Metadata = maps.Clone(source.Metadata)
	if copy.Metadata == nil {
		copy.Metadata = map[string]any{}
	}
	copy.Commands = make([]*cli.Command, len(source.Commands))
	for i, child := range source.Commands {
		copy.Commands[i] = cloneResourceCommand(child)
	}
	copy.Flags = slices.Clone(source.Flags)
	return &copy
}

func commandResourceName(command *cli.Command) string {
	if name, ok := command.Metadata[resourceCommandMetadata].(string); ok {
		return name
	}
	lineage := command.Lineage()
	if len(lineage) < 2 {
		return ""
	}
	resource := lineage[1]
	if name, ok := resource.Metadata[resourceCommandMetadata].(string); ok {
		return name
	}
	return resource.Name
}
