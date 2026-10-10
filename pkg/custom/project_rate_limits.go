package custom

import (
	"context"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

type projectRateLimitScopeKey struct{}

// Decorate before subgroup and task cloning so every project route agrees.
// The generated actions retain ownership of requests, pagination, and errors.
func configureProjectRateLimits(root *cli.Command) {
	resource := root.Command("admin:organization:projects:rate-limits")
	if resource == nil {
		return
	}
	resource.Usage = "List and update project rate limits by model."
	resource.Description = "Use list to find a model's rate-limit ID, then update its supported limits.\nThe existing list-rate-limits and update-rate-limit names remain available."
	for _, route := range []struct{ name, source, example, note string }{
		{"list", "list-rate-limits", "admin projects rate-limits list --project-id proj_demo",
			"Returns model names, rate-limit IDs, and limits with explicit units. Use --format json for API field names."},
		{"update", "update-rate-limit", "admin projects rate-limits update --project-id proj_demo --rate-limit-id rl_demo --max-tokens-per-1-minute 1000",
			"Use a rate-limit ID returned by list. --max-tokens-per-1-day is not supported by this command's API contract.\n--batch-1-day-max-input-tokens controls batch input tokens per day, not general tokens per day.\nThis command has no reset option. Omitted fields remain unchanged; zero and null are not reset shortcuts."},
	} {
		source := resource.Command(route.source)
		if source == nil || resource.Command(route.name) != nil {
			continue
		}
		if source.Metadata == nil {
			source.Metadata = map[string]any{}
		}
		source.Metadata["help-content"] = clihelp.Content{
			Description: route.note,
			Examples:    []clihelp.Example{{Command: route.example}},
		}
		if route.name == "list" && source.Action != nil {
			next := source.Action
			source.Action = func(ctx context.Context, command *cli.Command) error {
				project, present := "", command.IsSet("project-id")
				if present {
					project = command.String("project-id")
				} else if command.Args().Present() {
					project, present = command.Args().First(), true
				}
				if present {
					ctx = context.WithValue(ctx, projectRateLimitScopeKey{}, project)
				}
				return next(ctx, command)
			}
		}
		friendly := cloneResourceCommand(source)
		friendly.Name, friendly.Aliases, friendly.Hidden = route.name, nil, false
		resource.Commands = append(resource.Commands, friendly)
		markCompatibilityCommand(source)
	}
}

// Called only after successful exhaustion of a readable iterator.
func projectRateLimitEmptyText(opts ShowJSONOpts) (string, bool) {
	if opts.Context == nil || opts.OutputKind != OutputPageItem ||
		opts.Operation != "(resource) admin.organization.projects.rate_limits > (method) list_rate_limits" ||
		resolvedOutputFormat(opts) != "text" || opts.RawOutput || opts.Transform != "" {
		return "", false
	}
	project, ok := opts.Context.Value(projectRateLimitScopeKey{}).(string)
	if !ok || project == "" {
		return "", false
	}
	// Keep untrusted project IDs on one line, including newline and tab controls.
	return "No rate limits returned for " + readable.Text(jsonview.SanitizeTerminalString(project)) + ".", true
}
