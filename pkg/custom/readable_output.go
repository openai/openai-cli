package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
	"github.com/urfave/cli/v3"
)

func resolvedOutputFormat(opts ShowJSONOpts) string {
	return clioutput.ResolveFormat(clioutput.FormatOptions{Format: opts.Format, Transform: opts.Transform, RawOutput: opts.RawOutput})
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

// Preserve terminal detection when the interactive viewer uses guarded output.
type terminalOutputWriter struct {
	clioutput.Writer
	file *os.File
}

var _ term.File = terminalOutputWriter{}

func (w terminalOutputWriter) Fd() uintptr                { return w.file.Fd() }
func (w terminalOutputWriter) Read(p []byte) (int, error) { return w.file.Read(p) }
func (w terminalOutputWriter) Close() error               { return w.file.Close() }

// Render records as they arrive, keeping stream state separate from list records.
func showReadableIterator(iter jsonview.Iterator[outputJSON], opts ShowJSONOpts) error {
	if opts.OutputKind == OutputStreamEvent && opts.Transform == "" && !opts.RawOutput {
		return showReadableStream(iter, opts)
	}
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	emitted := false
	omitted := false
	for iter.Next() {
		value := clioutput.ApplyJSONPath(iter.Current().Result, opts.Transform)
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
		if err := writeOutputHint(opts, resourceSummaryHint); err != nil {
			return errors.Join(err, iter.Err())
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
