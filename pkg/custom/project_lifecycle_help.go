package custom

import (
	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

// Decorate canonical commands before subgroup and shortcut cloning.
func configureProjectLifecycle(root *cli.Command) {
	projects := root.Command("admin:organization:projects")
	if projects == nil {
		return
	}
	for name, content := range map[string]clihelp.Content{
		"create": {
			Description: "Use --residency only with a configuration your organization can access.\n" +
				"--geography is deprecated. Do not combine it with --residency.\n" +
				"After an interrupted request, check the project list before creating again.",
			Examples: []clihelp.Example{{Description: "Create a project:", Command: `admin projects create --name "Demo"`}},
		},
		"update": {
			Description: "Use the returned name and status to confirm the change.\n" +
				"After an interrupted request, retrieve the project before retrying.\n" +
				"Residency belongs to project creation. Key policy is a separate setting.",
			Examples: []clihelp.Example{{Description: "Rename a project:", Command: `admin projects update --project-id proj_demo --name "Renamed"`}},
		},
		"archive": {
			Description: "Archived projects cannot be used or updated. Archive is not a delete operation.\n" +
				"After an interrupted request, retrieve the project to check its status.\n" +
				"Use list --include-archived to include archived projects.",
			Examples: []clihelp.Example{{Description: "Archive a project:", Command: `admin projects archive --project-id proj_demo`}},
		},
	} {
		if command := projects.Command(name); command != nil {
			setCompleteHelpContent(command, content)
		}
	}
}
