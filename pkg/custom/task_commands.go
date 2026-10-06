package custom

import "github.com/urfave/cli/v3"

// Task routes reuse the decorated API handlers. Install them before help and
// parsing, so each invocation gets its own command nodes and displayed path.
func configureTaskCommands(root *cli.Command) {
	for _, task := range []struct {
		group, resource, name, usage string
		shortcut                     bool
	}{
		{"audio", "transcriptions", "transcribe", "Convert audio to text.", true},
		{"audio", "translations", "translate", "Translate audio to English text.", true},
		{"audio", "speech", "speak", "Generate speech from text.", true},
		{"files", "", "upload", "Upload an API file.", false},
	} {
		group := root.Command(task.group)
		if group == nil || group.Command(task.name) != nil {
			continue
		}
		resource := group
		if task.resource != "" {
			resource = group.Command(task.resource)
			if resource == nil || !structuralCommand(resource) {
				continue
			}
		}
		source := resource.Command("create")
		if source == nil {
			continue
		}
		command := cloneResourceCommand(source)
		command.Name, command.Aliases, command.Hidden = task.name, nil, false
		command.Usage = task.usage
		command.Metadata[resourceCommandMetadata] = resource.Name
		if name, ok := resource.Metadata[resourceCommandMetadata].(string); ok {
			command.Metadata[resourceCommandMetadata] = name
		}
		group.Commands = append(group.Commands, command)
		if task.resource == "" {
			markCompatibilityCommand(source)
		} else if len(resource.Commands) == 1 {
			markCompatibilityCommand(resource)
		}
		if task.shortcut && structuralCommand(group) {
			addTaskShortcut(root, command, task.name, task.group+" "+task.name)
		}
	}

	admin := root.Command("admin")
	if admin == nil {
		return
	}
	organization := admin.Command("organization")
	if organization == nil || !structuralCommand(organization) {
		return
	}
	complete := len(organization.Commands) > 0
	var projects *cli.Command
	for _, source := range organization.Commands {
		if commandNamesCollide(admin, source) {
			complete = false
			continue
		}
		promoted := cloneResourceCommand(source)
		admin.Commands = append(admin.Commands, promoted)
		if source.Name == "projects" {
			projects = promoted
		}
	}
	if complete {
		markCompatibilityCommand(organization)
	}
	if projects != nil && structuralCommand(admin) {
		if shortcut := addTaskShortcut(root, projects, "projects", "admin projects"); shortcut != nil {
			shortcut.Usage = "Manage organization projects; shortcut for admin projects."
		}
	}
}

// Do not skip future generated flags or hooks while shortening a route.
func structuralCommand(command *cli.Command) bool {
	return command.Action == nil && command.Before == nil && command.After == nil && command.ArgValidator == nil &&
		len(command.Flags) == 0 && len(command.Arguments) == 0 && !command.SkipFlagParsing &&
		!command.UseShortOptionHandling && command.StopOnNthArg == nil
}

func commandNamesCollide(parent, command *cli.Command) bool {
	for _, name := range append([]string{command.Name}, command.Aliases...) {
		if parent.Command(name) != nil {
			return true
		}
	}
	return false
}

func markCompatibilityCommand(command *cli.Command) {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Hidden = true
	command.Metadata["command-compatibility-alias"] = true
}

func addTaskShortcut(root, source *cli.Command, name, path string) *cli.Command {
	if root.Command(name) != nil {
		return nil
	}
	shortcut := cloneResourceCommand(source)
	shortcut.Name, shortcut.Aliases, shortcut.Hidden = name, nil, false
	shortcut.Usage = "Shortcut for " + path + ". " + source.Usage
	root.Commands = append(root.Commands, shortcut)
	return shortcut
}
