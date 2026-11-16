// Package webhooks owns guided webhook creation and webhook result guidance.
package webhooks

import (
	"context"
	"io"

	"github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// CommandOptions supplies assembly-owned request and diagnostic policies.
// PrepareDiscovery and DefaultRequestOptions must be supplied when configuring commands.
// Options belong to each command invocation; this package keeps no mutable policy globals.
type CommandOptions struct {
	PrepareDiscovery         func(*cli.Command) ([]option.RequestOption, error)
	DefaultRequestOptions    func(*cli.Command) []option.RequestOption
	WriteHint                func(context.Context, io.Writer, string) error
	ReadableTransportFailure func(*cli.Command, error) (string, bool)
}

// ResultOptions describes one selected readable result and its optional diagnostics.
type ResultOptions struct {
	output.ReadableOptions
	WriteHint func(context.Context, io.Writer, string) error
}
