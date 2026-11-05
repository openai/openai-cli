package webhooks

import (
	"strings"

	localcommands "github.com/openai/openai-cli/internal/commands"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func unavailableWebhookEvent(event string) bool {
	return strings.HasPrefix(event, "fine_tuning.")
}

// ValidateRequestEvents checks resolved JSON after flags and input files merge.
// Reject the complete request rather than silently changing its subscriptions.
func ValidateRequestEvents(command *cli.Command, body []byte) error {
	if localcommands.ResourceName(command) != "webhooks" {
		return nil
	}
	field := ""
	switch command.Name {
	case "create", "update":
		field = "event_types"
	case "test":
		field = "event_type"
	default:
		return nil
	}
	value := gjson.ParseBytes(body)
	if !gjson.ValidBytes(body) || !value.IsObject() {
		return nil
	}
	blocked := false
	// Inspect every occurrence, including escaped or repeated canonical keys.
	value.ForEach(func(key, value gjson.Result) bool {
		if key.Str != field {
			return true
		}
		if value.Type == gjson.String {
			blocked = unavailableWebhookEvent(value.Str)
		} else if value.IsArray() {
			value.ForEach(func(_, event gjson.Result) bool {
				blocked = event.Type == gjson.String && unavailableWebhookEvent(event.Str)
				return !blocked
			})
		}
		return !blocked
	})
	if blocked {
		// Keep private event names, endpoint IDs, and request fields out of errors.
		return &localcommands.LocalError{Message: "This webhook request contains events unavailable in the CLI. Use the Platform API directly."}
	}
	return nil
}
