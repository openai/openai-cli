package custom

import (
	"io"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/urfave/cli/v3"
)

// Auto means readable on every destination. Extraction is an explicit request
// for data and keeps its existing JSON/raw-string contract.
func resolvedOutputFormat(opts ShowJSONOpts) string {
	format := strings.ToLower(opts.Format)
	if format == "auto" || format == "" {
		if opts.Transform != "" || opts.RawOutput {
			return "json"
		}
		return "text"
	}
	return format
}

func configureReadableOutput(root *cli.Command) {
	configureReadableAudio(root)
	configureReadableGuidance(root)
	configureReadableSpeech(root)
	for _, flag := range root.Flags {
		if value, ok := flag.(*cli.StringFlag); ok {
			switch value.Name {
			case "format":
				value.Usage = "Readable text by default, including pipes. Use json for full API data. Formats: " + strings.Join(OutputFormats, ", ")
			case "format-error":
				value.Usage = "Error format on stderr; readable by default. Use json for API error details."
			}
		}
	}
}

func writeReadableResult(out io.Writer, output preparedOutput) error {
	return readable.WriteResult(out, output.Value, output.View)
}

func writeReadableText(out io.Writer, value string) error {
	return readable.WriteText(out, value)
}

// Keep iterator ownership and failure status at the shared output boundary.
// The readable library only owns formatting and streamed-text state.
func showReadableIterator(iter jsonview.Iterator[preparedOutput], opts ShowJSONOpts) error {
	writer := readable.NewStreamWriter(opts.Stdout)
	for iter.Next() {
		output := iter.Current()
		value := applyJSONPath(output.Value, opts.Transform)
		if err := writer.Write(value, output.View); err != nil {
			return &outputWriteError{err}
		}
	}
	if err := writer.Finish(); err != nil {
		return &outputWriteError{err}
	}
	if err := iter.Err(); err != nil {
		return err
	}
	if !writer.HasOutput() {
		if err := readable.WriteText(opts.Stdout, "No results."); err != nil {
			return &outputWriteError{err}
		}
	}
	return nil
}
