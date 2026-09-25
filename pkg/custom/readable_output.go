package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

// Automatic output is readable on every destination. Extraction keeps its
// existing JSON/raw-string contract, including when stdout is redirected.
func resolvedOutputFormat(opts ShowJSONOpts) string {
	format := strings.ToLower(opts.Format)
	if format == "" || format == "auto" {
		if opts.Transform != "" || opts.RawOutput {
			return "json"
		}
		return "text"
	}
	return format
}

func configureReadableOutput(root *cli.Command) {
	for _, flag := range root.Flags {
		value, ok := flag.(*cli.StringFlag)
		if !ok || value.Name != "format" {
			continue
		}
		value.Usage = "Readable text by default, including pipes. Use json for full API data. Formats: " + strings.Join(OutputFormats, ", ")
		previous := value.Action
		value.Action = func(ctx context.Context, command *cli.Command, format string) error {
			if previous != nil {
				if err := previous(ctx, command, format); err != nil {
					return err
				}
			}
			// Generated list handlers choose the raw page-envelope path before
			// reaching the renderer. Normalize here so RAW and raw agree.
			if normalized := strings.ToLower(format); normalized != format {
				return command.Set("format", normalized)
			}
			return nil
		}
	}
}

// outputWriter preserves cancellation separately from failures of the sink.
// Only actual sink errors may be treated as a closed output pipe by the pager.
type outputWriter struct {
	ctx context.Context
	out io.Writer
}

// Preserve terminal detection when the interactive viewer uses guarded output.
type terminalOutputWriter struct {
	outputWriter
	file *os.File
}

var _ term.File = terminalOutputWriter{}

func (w terminalOutputWriter) Fd() uintptr                { return w.file.Fd() }
func (w terminalOutputWriter) Read(p []byte) (int, error) { return w.file.Read(p) }
func (w terminalOutputWriter) Close() error               { return w.file.Close() }

func (w outputWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.out.Write(data)
	return w.result(n, len(data), err)
}

func (w outputWriter) WriteString(data string) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := io.WriteString(w.out, data)
	return w.result(n, len(data), err)
}

func (w outputWriter) result(n, size int, err error) (int, error) {
	if err == nil && n != size {
		err = io.ErrShortWrite
	}
	if err != nil {
		return n, &outputWriteError{err}
	}
	return n, w.ctx.Err()
}

// Render records as they arrive, keeping stream state separate from list records.
func showReadableIterator(iter jsonview.Iterator[outputJSON], opts ShowJSONOpts) error {
	if opts.OutputKind == OutputStreamEvent && opts.Transform == "" && !opts.RawOutput {
		return showReadableStream(iter, opts)
	}
	out := outputWriter{ctx: opts.Context, out: opts.Stdout}
	emitted := false
	omitted := false
	for iter.Next() {
		value := applyJSONPath(iter.Current().Result, opts.Transform)
		if opts.RawOutput && value.Type == gjson.String {
			// Keep raw strings byte-for-byte in pipes and escape terminal
			// controls using the same destination-aware formatter as ShowJSON.
			formatted, err := formatJSON(iter.Current().Result, opts)
			if err != nil {
				return errors.Join(err, iter.Err())
			}
			if _, err := out.Write(formatted); err != nil {
				return errors.Join(err, iter.Err())
			}
			emitted = true
			continue
		}
		if emitted {
			if _, err := io.WriteString(out, "\n"); err != nil {
				return errors.Join(err, iter.Err())
			}
		}
		hidden, err := writeReadableResource(out, value, opts)
		if err != nil {
			return errors.Join(err, iter.Err())
		}
		omitted = omitted || hidden
		emitted = true
	}
	if omitted {
		if err := readable.WriteText(out, resourceSummaryHint); err != nil {
			iterErr := iter.Err()
			// Closing stdout may suppress an output-only broken pipe, but must
			// never turn a failed page fetch into a successful command.
			if iterErr != nil && isOutputBrokenPipe(err) {
				return iterErr
			}
			return errors.Join(err, iterErr)
		}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if !emitted {
		return readable.WriteText(out, "No results.")
	}
	return opts.Context.Err()
}
