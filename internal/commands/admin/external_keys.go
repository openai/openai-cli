package admin

import (
	"strings"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// ValidateExternalKeyCreate checks the selected provider's required fields at
// the JSON encoding boundary, after flags, piped input, and file values merge.
// Shared required flags remain generated. The API owns value constraints and
// purpose-dependent key-version policy; do not require one provider's fields
// for another or reject future provider types here.
func ValidateExternalKeyCreate(command *cli.Command, body []byte) error {
	if command.Name != "create" || localcommands.ResourceName(command) != "admin:organization:external-keys" {
		return nil
	}
	value := gjson.ParseBytes(body)
	var required []string
	switch value.Get("type").String() {
	case "aws":
		required = []string{"external_id", "kms_arn", "role_arn"}
	case "gcp":
		required = []string{"audience", "kms_key_location", "kms_key_name", "kms_key_ring_name", "kms_project_id", "workload_identity_pool_id", "workload_identity_project_number", "workload_identity_provider_id"}
	case "azure":
		required = []string{"key_name", "tenant_id", "vault_uri"}
	default:
		return nil
	}
	var missing []string
	for _, field := range required {
		property := value.Get(field)
		if !property.Exists() || property.Type == gjson.Null {
			missing = append(missing, "--"+strings.ReplaceAll(field, "_", "-"))
		}
	}
	if len(missing) != 0 {
		// Only static schema field names enter diagnostics, never request values.
		return &localcommands.LocalError{Message: "Required external-key fields not set: " + strings.Join(missing, ", ")}
	}
	return nil
}
