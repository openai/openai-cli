package custom

import (
	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// This diagnostic is static: never include request values or credentials.
type projectAPIKeyUpdateError struct{}

func (*projectAPIKeyUpdateError) Error() string {
	return "provide at least one of --scope or --expires-in-seconds (scopes or expires_in_seconds in JSON/YAML input)"
}

// Validate the merged body so flags and JSON/YAML input follow the same rule.
// Keep this CLI guard outside generated request types; the server still owns
// value validation and expiry policy enforcement.
func validateProjectAPIKeyUpdate(command *cli.Command, body []byte) error {
	if command.Name != "update" || localcommands.ResourceName(command) != "admin:organization:projects:api-keys" {
		return nil
	}
	if !gjson.GetBytes(body, "scopes").Exists() && !gjson.GetBytes(body, "expires_in_seconds").Exists() {
		return &projectAPIKeyUpdateError{}
	}
	return nil
}
