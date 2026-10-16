package custom

import (
	"io"

	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-cli/internal/readable"
	"github.com/tidwall/gjson"
)

const resourceSummaryHint = "Full data: --format json."

// Resource projections apply only to readable results without field extraction.
// Report omissions so each response or list can explain them once.
func writeReadableResource(out io.Writer, value gjson.Result, opts ShowJSONOpts) (bool, error) {
	omitted := false
	if opts.Transform == "" && !opts.RawOutput {
		summary, hidden, err := transformers.SummarizeResource(opts.Context, value, transformers.Route{
			Operation: opts.Operation, OutputKind: opts.OutputKind,
		})
		if err != nil {
			return false, err
		}
		if summary.Exists() {
			value, omitted = summary, hidden
		}
	}
	return omitted, readable.Write(out, value)
}
