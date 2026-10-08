package custom

import (
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Agents creation and input can start durable work without a deduplication guarantee.
// Preserve the first outcome instead of silently submitting the operation again.
func agentsRequestOptions(command *cli.Command) []option.RequestOption {
	if command.Name != "create" {
		return nil
	}
	switch commandResourceName(command) {
	case "beta:agents", "beta:agents:sessions", "beta:agents:sessions:events":
		return []option.RequestOption{option.WithMaxRetries(0)}
	}
	return nil
}
