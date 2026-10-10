package custom

import (
	"context"
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

type keyInventoryScopeKey struct{}

// Decorate generated actions before subgroup and task commands clone them.
// The generated handler keeps ownership of all request options and pagination.
func configureKeyInventory(root *cli.Command) {
	for _, name := range []string{"admin:organization:admin-api-keys", "admin:organization:projects:api-keys", "admin:organization:projects:service-accounts"} {
		resource := root.Command(name)
		if resource == nil {
			continue
		}
		for _, method := range []string{"list", "retrieve"} {
			command := resource.Command(method)
			if command == nil {
				continue
			}
			command.Description += "\n\nReadable inventory preserves returned metadata. Timestamps use Unix seconds.\nMissing fields remain absent; null last-used values do not establish that a key was never used.\nUse --format json for full API data. Creation commands retain their one-time secret responses."
			if name == "admin:organization:admin-api-keys" {
				command.Description += "\nThis command reads the organization Admin API keys endpoint."
				if method == "list" {
					command.Usage = "List keys returned by the organization Admin API keys endpoint."
				}
			} else if name == "admin:organization:projects:api-keys" {
				command.Description += "\nOwner project access describes the owner's access, not key validity."
				if method == "list" {
					command.Description += "\nList visibility follows the API default; --owner-project-access any requests all enabled project keys."
				}
			} else {
				command.Description += "\nA service-account role describes membership, not credential validity."
			}
			next := command.Action
			command.Action = func(ctx context.Context, command *cli.Command) error {
				// Generated actions resolve positional path parameters after this
				// wrapper. Read the selected command when output actually starts.
				return next(context.WithValue(ctx, keyInventoryScopeKey{}, func() string { return command.String("project-id") }), command)
			}
		}
	}
}

func keyInventoryScope(opts ShowJSONOpts) (string, string) {
	resource := transformers.KeyInventoryResource(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
	project := ""
	if scope, ok := opts.Context.Value(keyInventoryScopeKey{}).(func() string); ok {
		project = scope()
	}
	if project == "" {
		project = "selected project"
	}
	switch resource {
	case "admin.organization.admin_api_keys":
		return "API key", "organization Admin-key endpoint"
	case "admin.organization.projects.api_keys":
		return "Project API key", project
	case "admin.organization.projects.service_accounts":
		return "Service account", project
	}
	return "", ""
}

func writeKeyInventory(out io.Writer, value gjson.Result, opts ShowJSONOpts) (bool, error) {
	kind, scope := keyInventoryScope(opts)
	if kind == "" {
		return false, nil
	}
	projected, _, err := transformers.ProjectKeyInventory(opts.Context, value, transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
	if err != nil {
		return true, err
	}
	if err := readable.WriteText(out, kind+" · "+scope); err != nil {
		return true, err
	}
	return true, readable.Write(out, projected)
}

// The shared empty-list boundary calls this only after checking iterator errors.
func keyInventoryEmptyMessage(opts ShowJSONOpts) string {
	if opts.OutputKind != OutputPageItem || opts.Transform != "" || opts.RawOutput {
		return ""
	}
	kind, scope := keyInventoryScope(opts)
	if kind == "" {
		return ""
	}
	if kind == "API key" {
		return "No keys returned by the organization Admin-key endpoint."
	}
	return "No " + strings.ReplaceAll(strings.ToLower(kind), "api", "API") + "s returned for " + scope + "."
}
