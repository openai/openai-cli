package custom

import (
	"github.com/openai/openai-cli/internal/commands/admin"
	"github.com/urfave/cli/v3"
)

func registerAdminSetup(root *cli.Command) {
	admin.RegisterSetup(root, admin.Options{
		DefaultRequestOptions: GetDefaultRequestOptions,
		TextOutput: func(root *cli.Command) bool {
			return resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text" && errorOutputFormat(root) == "text"
		},
		IsTerminal:         isTerminal,
		DiagnosticsAllowed: commandAllowsOutputDiagnostics,
	})
}
