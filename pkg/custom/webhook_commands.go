package custom

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/openai/openai-cli/internal/clihelp"
	"github.com/urfave/cli/v3"
)

type webhookCommandKey struct{}

type webhookCommandContext struct {
	operation  string
	invocation fileInvocation
}

// Configure the existing API commands before subgroup cloning.
func configureWebhookCommands(root *cli.Command) {
	webhooks := root.Command("webhooks")
	if webhooks == nil {
		return
	}
	webhooks.Usage = "Create webhook endpoints, choose events, and test delivery."
	webhooks.Description = "OpenAI sends event notifications to your server's HTTPS URL.\n" +
		"Start with create to choose a name, URL, and events in a terminal.\n" +
		"Use event-types list to discover events available to your project."
	webhookHelp(webhooks, clihelp.Content{
		Description: webhooks.Description,
		Examples:    []clihelp.Example{{Description: "Create an endpoint with guided settings:", Command: "webhooks create"}},
	})
	guidance := map[string]clihelp.Content{
		"create": {
			InputNote: "Run without request flags in a terminal for guided creation.\nFor scripts, supply --name, --url, and at least one --event-type, or pipe JSON/YAML.",
			Description: "Use a public HTTPS receiver URL. Repeat --event-type to select multiple events.\n" +
				"The guided flow loads current project events and asks before creating anything.\n" +
				"Save the returned signing secret securely; create and rotate-secret are the only ways to receive it.\n" +
				"Verify webhook signatures in your receiver before processing events.",
			Examples: []clihelp.Example{{Description: "Create an endpoint for completed responses:", Command: `webhooks create --name "Response notifications" --url https://example.com/webhook --event-type response.completed`}},
		},
		"list": {
			Description: "Lists endpoints in the authenticated project, newest first.\nUse create to add an endpoint, retrieve to inspect one, and test to send a sample event.",
			Examples:    []clihelp.Example{{Description: "Find your endpoint ID:", Command: "webhooks list"}},
		},
		"retrieve": {
			Description: "Inspect the endpoint URL and subscribed events.\nExisting signing secrets are not returned; use your securely saved secret.",
			Examples:    []clihelp.Example{{Description: "Inspect an endpoint:", Command: "webhooks retrieve --webhook-endpoint-id whe_example"}},
		},
		"update": {
			Description: "Change the endpoint name, URL, or subscribed events.\nWhen supplied, --event-type replaces the complete event set. Repeat it for every event you want to keep.",
			Examples:    []clihelp.Example{{Description: "Change the receiver URL:", Command: "webhooks update --webhook-endpoint-id whe_example --url https://example.com/webhook"}},
		},
		"test": {
			Description: "Send one sample event to an existing receiver. Use event-types list to discover event names.\n" +
				"A receiver 2xx response confirms receipt, not application processing.\n" +
				"Receiver failures keep exit status 0 when the test API request completes.\n" +
				"Readable output includes recovery guidance; --format json preserves the API response.",
			Examples: []clihelp.Example{{Description: "Send a completed-response sample:", Command: "webhooks test --webhook-endpoint-id whe_example --event-type response.completed"}},
		},
		"rotate-secret": {
			Description: "Returns a new signing secret once. Save it securely and update your receiver.\n" +
				"Use --keep-old-secret-active-for-24-hours for an overlap while updating receivers.\n" +
				"Without that flag, the previous secret becomes invalid immediately.",
			Examples: []clihelp.Example{{Description: "Rotate with a 24-hour transition:", Command: "webhooks rotate-secret --webhook-endpoint-id whe_example --keep-old-secret-active-for-24-hours"}},
		},
		"delete": {
			Description: "Removes this endpoint from the project. Verify the ID with retrieve before deleting it.",
			Examples:    []clihelp.Example{{Description: "Delete an endpoint you no longer use:", Command: "webhooks delete --webhook-endpoint-id whe_example"}},
		},
	}
	for name, content := range guidance {
		command := webhooks.Command(name)
		if command == nil {
			continue
		}
		webhookHelp(command, content)
		next := command.Action
		if name == "create" {
			next = webhookCreateWorkflow(next)
		}
		command.Action = webhookActionContext(name, next)
	}
	if catalog := root.Command("webhooks:event-types"); catalog != nil {
		catalog.Usage = "Discover webhook events available to your project."
		if list := catalog.Command("list"); list != nil {
			webhookHelp(list, clihelp.Content{
				Description: "Loads the current event catalog for your authenticated project.\nUse these exact names with --event-type. Events may vary by project and API availability.",
				Examples:    []clihelp.Example{{Description: "Show available event names:", Command: "webhooks event-types list"}},
			})
			list.Action = webhookActionContext("event-types", list.Action)
		}
	}
}

func webhookHelp(command *cli.Command, content clihelp.Content) {
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	command.Metadata["help-content"] = content
}

func webhookActionContext(operation string, next cli.ActionFunc) cli.ActionFunc {
	return func(ctx context.Context, command *cli.Command) error {
		invocation := fileInvocation{display: errorHelpInvocation(command.Root())}
		invocation.requestArgs, invocation.omitHint = fileReceiptRequestOptions(command.Root())
		invocation.goRun = invocation.display == "go run ./cmd/"+command.Root().Name
		if len(os.Args) > 0 {
			invocation.executable = os.Args[0]
			if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") != command.Root().Name ||
				strings.IndexFunc(os.Args[0], unicode.IsControl) >= 0 {
				invocation.display, invocation.goRun = os.Args[0], false
			}
		}
		if invocation.goRun {
			var err error
			invocation.goRunDir, err = os.Getwd()
			invocation.omitHint = invocation.omitHint || err != nil
		}
		ctx = context.WithValue(ctx, webhookCommandKey{}, webhookCommandContext{operation, invocation})
		if err := next(ctx, command); err != nil {
			return err
		}
		if operation == "list" && webhookReadableCommand(command) {
			create := webhookFollowupCommand(ctx, "webhooks", "create")
			advice := "Next: use retrieve with an endpoint ID to inspect its URL and events."
			if create != "" {
				advice += "\nCreate another endpoint: " + create
			} else {
				advice += "\nTo create an endpoint, use webhooks create with your original authentication and project settings."
			}
			if help := webhookFollowupCommand(ctx, "webhooks", "--help"); help != "" {
				advice += "\nSee all operations: " + help
			}
			return writeOutputHint(ShowJSONOpts{Context: ctx, Stderr: os.Stderr}, advice)
		}
		return nil
	}
}

func webhookReadableCommand(command *cli.Command) bool {
	root := command.Root()
	return root.String("transform") == "" && !root.Bool("raw-output") &&
		resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
}
