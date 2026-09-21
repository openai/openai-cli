package custom

import (
	"context"
	"fmt"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// These generated actions intentionally have no response body. Keep their API
// contract in explicit data modes, while confirming successful human actions.
var readableBodylessActions = map[string]struct{ verb, object, id string }{
	"responses delete":        {"Deleted", "response", "response-id"},
	"beta:responses delete":   {"Deleted", "response", "response-id"},
	"containers delete":       {"Deleted", "container", "container-id"},
	"containers:files delete": {"Deleted", "container file", "file-id"},
	"realtime:calls accept":   {"Accepted", "call", "call-id"},
	"realtime:calls hangup":   {"Ended", "call", "call-id"},
	"realtime:calls refer":    {"Requested transfer for", "call", "call-id"},
	"realtime:calls reject":   {"Rejected", "call", "call-id"},
	"live:sessions accept":    {"Accepted", "session", "session-id"},
	"live:sessions hangup":    {"Ended", "session", "session-id"},
	"live:sessions refer":     {"Requested transfer for", "session", "session-id"},
	"live:sessions reject":    {"Rejected", "session", "session-id"},
}

type readableCommandError struct {
	command *cli.Command
	err     error
}

func (e *readableCommandError) Error() string { return e.err.Error() }
func (e *readableCommandError) Unwrap() error { return e.err }

func configureReadableGuidance(root *cli.Command) {
	for _, resource := range root.Commands {
		if resource.Category != "API RESOURCE" {
			continue
		}
		for _, command := range resource.Commands {
			if command.Action == nil {
				continue
			}
			next := command.Action
			success, confirms := readableBodylessActions[resource.Name+" "+command.Name]
			command.Action = func(ctx context.Context, command *cli.Command) error {
				if err := next(ctx, command); err != nil {
					return &readableCommandError{command: command, err: err}
				}
				format := resolvedOutputFormat(ShowJSONOpts{Format: root.String("format"), Transform: root.String("transform"), RawOutput: root.Bool("raw-output")})
				if confirms && format == "text" && root.String("transform") == "" && !root.Bool("raw-output") {
					// Quoting keeps a caller-supplied ID on one line. Text additionally
					// escapes bidi controls that Go's ordinary quoting leaves visible.
					message := fmt.Sprintf("%s %s %q.", success.verb, success.object, readable.Text(command.String(success.id)))
					return writeReadableText(root.Writer, message)
				}
				return nil
			}
		}
	}
}
