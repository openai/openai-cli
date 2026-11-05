package request

import (
	"github.com/openai/openai-cli/internal/requestflag"
	"github.com/urfave/cli/v3"
)

// ConfigureRootFlags makes only root request configuration inheritable.
// Endpoint request flags retain their local scope, including a body --project.
func ConfigureRootFlags(root *cli.Command) {
	for i, flag := range root.Flags {
		if url, ok := flag.(*cli.StringFlag); ok && url.Name == "base-url" {
			root.Flags[i] = &baseURLFlag{StringFlag: url}
			continue
		}
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
	explicitlySet    bool
	credentialInputs []string
}

func (*RootRequestFlag) IsLocal() bool { return false }

func (f *RootRequestFlag) PreParse() error {
	f.credentialInputs = nil
	f.explicitlySet = false
	return f.Flag.PreParse()
}

// Environment fallback calls the embedded Flag.Set directly. Only parser or
// Command.Set assignments pass through this wrapper, including explicit empties.
func (f *RootRequestFlag) Set(name, value string) error {
	if err := f.Flag.Set(name, value); err != nil {
		return err
	}
	switch f.Name {
	case "api-key", "admin-api-key", "webhook-secret":
		f.credentialInputs = append(f.credentialInputs, value)
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

// baseURLFlag keeps credentials from an earlier --base-url private even if a later flag replaces it.
type baseURLFlag struct {
	*cli.StringFlag
	credentialInputs []string
}

func (f *baseURLFlag) CLIStringFlag() *cli.StringFlag { return f.StringFlag }

func (f *baseURLFlag) PreParse() error {
	f.credentialInputs = nil
	return f.StringFlag.PreParse()
}

func (f *baseURLFlag) Set(name, value string) error {
	if err := f.StringFlag.Set(name, value); err != nil {
		return err
	}
	f.credentialInputs = appendURLCredentials(f.credentialInputs, value)
	return nil
}
