package custom

import "github.com/urfave/cli/v3"

// Decorate original command objects before subgroup and task aliases clone them.
// Requests remain entirely owned by the generated actions.
func configureDataControlsHelp(root *cli.Command) {
	for _, name := range []string{"admin:organization:data-retention", "admin:organization:projects:data-retention"} {
		if resource := root.Command(name); resource != nil {
			for _, command := range resource.Commands {
				command.Description = "This response reports configured retention. It does not resolve effective retention."
			}
		}
	}
	if resource := root.Command("admin:organization:external-storage"); resource != nil {
		for _, command := range resource.Commands {
			switch command.Name {
			case "create":
				command.Description = "Registration does not complete validation. Inspect the returned status before taking further action."
			case "retrieve", "list":
				command.Description = "Inspection reads the saved validation result; it does not run validation. Validated does not establish continuous storage health."
			case "validate":
				command.Description = "Validation writes cloud test objects and activates customer-managed retention on success. It requires authorized cloud storage and organization Admin access. A pending response does not mean validation completed."
			}
		}
	}
}
