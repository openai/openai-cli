package custom

import "github.com/urfave/cli/v3"

// describeAgentsCommands supplies workflow summaries before subgroup cloning.
// Generated handlers retain command flags, request parsing, and API operations.
func describeAgentsCommands(root *cli.Command) {
	for name, usage := range map[string]string{
		"beta:agents":                                "Create and manage reusable agents.",
		"beta:agents:sessions":                       "Start, inspect, and manage agent sessions.",
		"beta:agents:sessions:events":                "Submit input and observe live session events.",
		"beta:agents:sessions:items":                 "Inspect saved session input and output.",
		"beta:agents:sessions:turns":                 "Inspect root turns and their outcomes.",
		"beta:agents:sessions:turns:items":           "Inspect the items from one root turn.",
		"beta:agents:sessions:artifacts":             "Find and download published session artifacts.",
		"beta:agents:sessions:traces":                "Retrieve published session traces.",
		"beta:agents:sessions:subagents":             "Inspect delegated agents.",
		"beta:agents:sessions:subagents:items":       "Inspect saved subagent input and output.",
		"beta:agents:sessions:subagents:turns":       "Inspect subagent turns and their outcomes.",
		"beta:agents:sessions:subagents:turns:items": "Inspect the items from one subagent turn.",
	} {
		if resource := root.Command(name); resource != nil {
			resource.Usage = usage
		}
	}
	if resource := root.Command("beta:agents:sessions:events"); resource != nil {
		if stream := resource.Command("stream"); stream != nil {
			stream.Description = "Observe live events without replay. Ctrl+C stops local observation; remote work continues.\nUse sessions retrieve and sessions items list after a disconnect."
		}
	}
}
