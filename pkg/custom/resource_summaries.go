package custom

import (
	"io"

	"github.com/openai/openai-cli/internal/readable"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/gjson"
)

// Resource projections apply only to readable results without field extraction.
// Keep the explanatory hint out of API values and machine-readable formats.
func writeReadableResource(out io.Writer, value gjson.Result, opts ShowJSONOpts) error {
	omitted := false
	if opts.Transform == "" && !opts.RawOutput {
		summary, hidden, err := transformers.SummarizeResource(opts.Context, value, transformers.Route{
			Operation: opts.Operation, OutputKind: opts.OutputKind,
		})
		if err != nil {
			return err
		}
		if summary.Exists() {
			value, omitted = summary, hidden
		}
	}
	if err := readable.Write(out, value); err != nil {
		return err
	}
	if omitted {
		return readable.WriteText(out, "Summary; use --format json for full data.")
	}
	return nil
}
