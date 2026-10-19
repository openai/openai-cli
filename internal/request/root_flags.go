package request

import (
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureRootFlags makes only root request configuration inheritable.
// Endpoint request flags retain their local scope, including a body --project.
func ConfigureRootFlags(root *cli.Command) {
	for i, flag := range root.Flags {
		request, ok := flag.(*requestflag.Flag[string])
		if !ok {
			continue
		}
		switch request.Name {
		case "api-key", "admin-api-key", "webhook-secret":
			// The help decorator can run before or after root registration.
			request.HideDefault = true
			root.Flags[i] = &RootRequestFlag{Flag: request}
		case "organization", "project":
			root.Flags[i] = &RootRequestFlag{Flag: request}
		}
	}
}

// RootRequestFlag preserves request metadata while sharing root options with subcommands.
type RootRequestFlag struct {
	*requestflag.Flag[string]
}

func (*RootRequestFlag) IsLocal() bool { return false }

// RequestFlag exposes the existing metadata for help decoration and redaction.
func (f *RootRequestFlag) RequestFlag() *requestflag.Flag[string] { return f.Flag }
