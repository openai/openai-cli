package app

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/openai/openai-cli/internal/apiquery"
	batchcommands "github.com/openai/openai-cli/internal/commands/batches"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-go/v3/option"
	"github.com/tidwall/gjson"
	"github.com/tidwall/pretty"
	"github.com/urfave/cli/v3"
)

func configureBatchCommands(root *cli.Command) {
	batchcommands.Configure(root, batchcommands.Options{
		DefaultRequestOptions: GetDefaultRequestOptions,
		RequestOptions: func(cmd *cli.Command, stdin io.Reader) ([]option.RequestOption, error) {
			return flagOptionsWithStdin(cmd, apiquery.NestedQueryFormatBrackets, apiquery.ArrayQueryFormatBrackets, EmptyBody, false, stdin)
		},
		ShowResult: func(ctx context.Context, cmd *cli.Command, batch gjson.Result, operation string, out, diagnostics io.Writer) error {
			root := cmd.Root()
			return ShowJSON(batch, ShowJSONOpts{
				CallerHandlesSignals: true,
				Context:              ctx, Operation: "(resource) batches > (method) " + operation, OutputKind: OutputResponse,
				Format: root.String("format"), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"),
				Transform: root.String("transform"), Title: "batches " + operation, Stdout: out, Stderr: diagnostics,
			})
		},
		ShowRecords:     showBatchRecords,
		HumanTerminal:   batchHumanTerminal,
		APIErrorMessage: readableAPIErrorMessage,
	})
}

func showBatchRecords(ctx context.Context, command *cli.Command, records batchcommands.RecordOutput, out, diagnostics io.Writer) error {
	root := command.Root()
	operation := records.Operation
	if operation == "" {
		operation = "results"
	}
	opts := ShowJSONOpts{
		CallerHandlesSignals: true, Context: ctx, Operation: "(resource) batches > (method) " + operation, OutputKind: OutputResponse,
		Format: root.String("format"), ExplicitFormat: root.IsSet("format"), RawOutput: root.Bool("raw-output"),
		Transform: root.String("transform"), Title: "batches " + operation, Stdout: out, Stderr: diagnostics,
	}
	writeJSON := records.WriteJSON
	format := resolvedOutputFormat(opts)
	if format == "explore" && !isTerminal(out) && opts.Transform == "" {
		if opts.ExplicitFormat && outputDiagnosticsAllowed(ctx) {
			if _, err := (clioutput.Writer{Context: ctx, Sink: diagnostics}).WriteString(warningExploreNotSupported); err != nil {
				return err
			}
		}
		format = "json"
	}
	if opts.Transform != "" || format == "explore" || format == "pretty" || format == "yaml" {
		// Arbitrary extraction and interactive/static trees need a complete value.
		// Keep that explicit path, without the old retained record slices or marshal copy.
		var document strings.Builder
		if err := writeJSON(&document); err != nil {
			return err
		}
		return ShowJSON(gjson.Parse(document.String()), opts)
	}
	if format == "text" {
		if records.Visit != nil {
			return showReadableBatchRecords(ctx, records, out, diagnostics)
		}
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		go func() { err := writeJSON(writer); _ = writer.CloseWithError(err); done <- err }()
		err := readable.WriteJSONStream(ctx, clioutput.Writer{Context: ctx, Sink: out}, reader, records.TemporaryFile)
		_ = reader.CloseWithError(err)
		return errors.Join(err, <-done)
	}
	if format != "json" && format != "jsonl" && format != "raw" {
		return ShowJSON(gjson.Parse("{}"), opts)
	}
	stream := batchJSONOutput{
		out:    bufio.NewWriterSize(clioutput.Writer{Context: ctx, Sink: out}, 32*1024),
		indent: format == "json", raw: format == "raw", color: format != "raw" && clioutput.UseColors(isTerminal(out)),
	}
	if err := writeJSON(&stream); err != nil {
		return err
	}
	if err := stream.out.WriteByte('\n'); err != nil {
		return err
	}
	return stream.out.Flush()
}

// batchJSONOutput formats an already validated document incrementally.
// The stack tracks nesting only; payload strings and numbers never accumulate.
type batchJSONOutput struct {
	out                                     *bufio.Writer
	indent, color, raw                      bool
	stack                                   []bool
	key, pending, inString, escaped, scalar bool
	escapeDigits                            int
	style                                   [2]string
}

func (w *batchJSONOutput) text(value string) error { _, err := w.out.WriteString(value); return err }
func (w *batchJSONOutput) Flush() error            { return w.out.Flush() }
func (w *batchJSONOutput) line(depth int) error {
	if !w.indent {
		return nil
	}
	return w.text("\n" + strings.Repeat("  ", depth))
}
func (w *batchJSONOutput) paint(value byte, style [2]string) error {
	if w.color {
		if err := w.text(style[0]); err != nil {
			return err
		}
	}
	if err := w.out.WriteByte(value); err != nil {
		return err
	}
	if w.color {
		return w.text(style[1])
	}
	return nil
}
func (w *batchJSONOutput) Write(data []byte) (int, error) {
	if w.raw {
		return w.out.Write(data)
	}
	for i, b := range data {
		if err := w.byte(b); err != nil {
			return i, err
		}
	}
	return len(data), nil
}
func (w *batchJSONOutput) byte(b byte) error {
	if w.inString {
		if w.escapeDigits > 0 {
			if err := w.out.WriteByte(b); err != nil {
				return err
			}
			w.escapeDigits--
			if w.escapeDigits == 0 && w.color {
				return w.text(pretty.TerminalStyle.Escape[1] + w.style[0])
			}
			return nil
		}
		if w.escaped {
			w.escaped = false
			if err := w.out.WriteByte(b); err != nil {
				return err
			}
			if b == 'u' {
				w.escapeDigits = 4
			} else if w.color {
				return w.text(pretty.TerminalStyle.Escape[1] + w.style[0])
			}
			return nil
		}
		if b == '\\' {
			w.escaped = true
			if w.color {
				if err := w.text(pretty.TerminalStyle.Escape[0]); err != nil {
					return err
				}
			}
		}
		if err := w.out.WriteByte(b); err != nil {
			return err
		}
		if b == '"' {
			w.inString = false
			if w.color {
				return w.text(w.style[1])
			}
		}
		return nil
	}
	if b == ' ' || b == '\t' || b == '\r' || b == '\n' {
		return nil
	}
	if w.scalar && strings.ContainsRune(",]}:", rune(b)) {
		w.scalar = false
		if w.color {
			if err := w.text(w.style[1]); err != nil {
				return err
			}
		}
	}
	if w.pending && b != ']' && b != '}' {
		if err := w.line(len(w.stack)); err != nil {
			return err
		}
		w.pending = false
	}
	switch b {
	case '{', '[':
		if err := w.paint(b, pretty.TerminalStyle.Brackets); err != nil {
			return err
		}
		w.stack = append(w.stack, b == '{')
		w.key, w.pending = b == '{', true
	case '}', ']':
		w.stack = w.stack[:len(w.stack)-1]
		if !w.pending {
			if err := w.line(len(w.stack)); err != nil {
				return err
			}
		}
		w.pending = false
		return w.paint(b, pretty.TerminalStyle.Brackets)
	case ',':
		if err := w.out.WriteByte(b); err != nil {
			return err
		}
		w.key = len(w.stack) > 0 && w.stack[len(w.stack)-1]
		return w.line(len(w.stack))
	case ':':
		w.key = false
		if w.indent {
			return w.text(": ")
		}
		return w.out.WriteByte(b)
	case '"':
		w.style = pretty.TerminalStyle.String
		if w.key {
			w.style = pretty.TerminalStyle.Key
		}
		w.inString = true
		if w.color {
			if err := w.text(w.style[0]); err != nil {
				return err
			}
		}
		return w.out.WriteByte(b)
	default:
		if !w.scalar {
			w.scalar = true
			w.style = pretty.TerminalStyle.Number
			switch b {
			case 't':
				w.style = pretty.TerminalStyle.True
			case 'f':
				w.style = pretty.TerminalStyle.False
			case 'n':
				w.style = pretty.TerminalStyle.Null
			}
			if w.color {
				if err := w.text(w.style[0]); err != nil {
					return err
				}
			}
		}
		return w.out.WriteByte(b)
	}
	return nil
}

func batchHumanTerminal(ctx context.Context, root *cli.Command) bool {
	return outputDiagnosticsAllowed(ctx) && isTerminal(root.Writer) && isTerminal(os.Stderr) && !root.Bool("debug") &&
		!root.Bool("raw-output") && root.String("transform") == "" &&
		root.String("transform-error") == "" && errorOutputFormat(root) == "text" &&
		resolvedOutputFormat(ShowJSONOpts{Format: root.String("format")}) == "text"
}

func showBatchNextCommand(batch gjson.Result, opts ShowJSONOpts) error {
	opts.setDefaults()
	return batchcommands.ShowNextCommand(batch, batchcommands.HintOptions{
		Context: opts.Context, Route: opts.Operation, Response: opts.OutputKind == OutputResponse,
		Terminal: isTerminal(opts.Stdout) && isTerminal(opts.Stderr), Transform: opts.Transform,
		RawOutput: opts.RawOutput, Format: resolvedOutputFormat(opts), Stderr: opts.Stderr,
		HumanTerminal: batchHumanTerminal,
	})
}
