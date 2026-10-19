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
	// Request flags retain presence and counts across parsing. Reset only these
	// root wrappers, preserving their current public definition and pointer.
	flag := f.Flag
	*flag = requestflag.Flag[string]{
		Name: flag.Name, Category: flag.Category, DefaultText: flag.DefaultText,
		HideDefault: flag.HideDefault, Usage: flag.Usage, Sources: flag.Sources,
		Required: flag.Required, Hidden: flag.Hidden, Default: flag.Default,
		Aliases: flag.Aliases, Validator: flag.Validator,
		QueryPath: flag.QueryPath, HeaderPath: flag.HeaderPath, BodyPath: flag.BodyPath,
		BodyRoot: flag.BodyRoot, PathParam: flag.PathParam, Const: flag.Const,
		FileInput: flag.FileInput, DataAliases: flag.DataAliases,
	}
	return flag.PreParse()
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

// ExplicitRequestFlag preserves presence through wrappers that embed this flag.
func (f *RootRequestFlag) ExplicitRequestFlag() bool { return f.explicitlySet }
