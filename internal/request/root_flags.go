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
	explicitlySet bool
}

func (*RootRequestFlag) IsLocal() bool { return false }

func (f *RootRequestFlag) PreParse() error {
	f.explicitlySet = false
	return f.Flag.PreParse()
}

// Environment fallback calls the embedded Flag.Set directly. Only parser or
// Command.Set assignments pass through this wrapper, including explicit empties.
func (f *RootRequestFlag) Set(name, value string) error {
	if err := f.Flag.Set(name, value); err != nil {
		return err
	}
	f.explicitlySet = true
	return nil
}

// RequestFlag exposes the existing metadata for help decoration and redaction.
func (f *RootRequestFlag) RequestFlag() *requestflag.Flag[string] { return f.Flag }

// ExplicitRootFlag distinguishes command-line assignments from inherited environment values.
func ExplicitRootFlag(root *cli.Command, name string) bool {
	for _, flag := range root.Flags {
		if f, ok := flag.(*RootRequestFlag); ok && f.Name == name {
			return f.explicitlySet
		}
	}
	return false
}
