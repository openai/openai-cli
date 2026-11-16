// Package admin owns admin credential requirements and secure interactive setup.
package admin

import (
	"io"

	"github.com/openai/openai-go/v3/option"
	"github.com/urfave/cli/v3"
)

// Options supplies application policies at the existing setup call sites.
// All callbacks are required by RegisterSetup.
type Options struct {
	DefaultRequestOptions func(*cli.Command) []option.RequestOption
	TextOutput            func(*cli.Command) bool
	IsTerminal            func(io.Writer) bool
	DiagnosticsAllowed    func(*cli.Command) bool
}
