package batches

import (
	"context"
	"io"
	"os"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Options connects batch workflows to application request and presentation policies.
// Assembly supplies every callback, retaining feature middleware and output policy.
type Options struct {
	DefaultRequestOptions func(*cli.Command) []option.RequestOption
	RequestOptions        func(*cli.Command, io.Reader) ([]option.RequestOption, error)
	ShowResult            func(context.Context, *cli.Command, gjson.Result, string, io.Writer, io.Writer) error
	HumanTerminal         func(context.Context, *cli.Command) bool
	APIErrorMessage       func(*cli.Command, error, *openai.Error) string
}

type workflow struct{ options Options }

// Configure registers local batch workflows around the generated commands.
func Configure(root *cli.Command, options Options) {
	(workflow{options: options}).configureBatchCommands(root)
}

// HintOptions carries the final destination and format policy for batch receipts.
type HintOptions struct {
	Context       context.Context
	Route         string
	Response      bool
	Terminal      bool
	Transform     string
	RawOutput     bool
	Format        string
	Stderr        io.Writer
	HumanTerminal func(context.Context, *cli.Command) bool
}

func isTerminal(w io.Writer) bool {
	switch v := w.(type) {
	case terminal.BorrowedWriter:
		return term.IsTerminal(v.File.Fd())
	case *os.File:
		return term.IsTerminal(v.Fd())
	default:
		return false
	}
}
