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
	CreateRequestOptions  func(*cli.Command, io.Reader) ([]option.RequestOption, gjson.Result, error)
	ShowResult            func(context.Context, *cli.Command, gjson.Result, string, io.Writer, io.Writer) error
	ShowRecords           func(context.Context, *cli.Command, RecordOutput, io.Writer, io.Writer) error
	ShowBrowserDetails    func(context.Context, gjson.Result, io.Writer, func(RecordTextStyle)) error
	HumanTerminal         func(context.Context, *cli.Command) bool
	APIErrorMessage       func(*cli.Command, error, *openai.Error) string
}

// RecordOutput lets presentation stream validated records and borrow private
// scratch storage for unusually large labels. The workflow owns all cleanup.
type RecordOutput struct {
	Operation             string
	WriteJSON             func(io.Writer) error
	TemporaryFile         func() (*os.File, func() error, error)
	BatchID, File, FileID string
	Visit                 func(func(RecordItem) error) error
	// StopSource interrupts an active download after presentation fails.
	// Stored records need no interruption and leave this callback nil.
	StopSource context.CancelCauseFunc
	// Detail omits repeated file metadata inside an already labeled browser pane.
	Detail bool
	// Decorate marks trusted presentation spans in one bounded browser projection.
	// Raw, standalone, and complete streaming fallback output do not use it.
	Decorate func(RecordTextStyle)
}

type RecordTextStyle struct {
	Start, End int64
	Role       string
}

// RecordItem exposes one stored record, or a group's matching metadata.
// WriteJSON streams that item without retaining its payload or duplicate group.
// The visitor must finish reading before its callback returns.
type RecordItem struct {
	Kind      string
	WriteJSON func(io.Writer) error
}

type workflow struct {
	options       Options
	browserOutput io.Writer
}

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
