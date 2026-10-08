package custom

import "github.com/openai/openai-cli/pkg/transformers"

// writeAgentsStreamSummaryHint runs once after readable stream output finishes.
// The shared policy owns quiet mode, structured errors, and diagnostic failures.
func writeAgentsStreamSummaryHint(opts ShowJSONOpts, omitted bool) error {
	if !omitted || !transformers.IsAgentsStream(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}) ||
		resolvedOutputFormat(opts) != "text" || opts.Transform != "" || opts.RawOutput {
		return nil
	}
	opts.setDefaults()
	return writeOutputHint(opts, "Some fields omitted. Use --format jsonl for complete events.")
}
