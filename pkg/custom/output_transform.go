package custom

import (
	"strings"

	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
)

type OutputKind = transformers.OutputKind

const (
	OutputUnspecified = transformers.OutputUnspecified
	OutputResponse    = transformers.OutputResponse
	OutputPageItem    = transformers.OutputPageItem
	OutputStreamEvent = transformers.OutputStreamEvent
)

type transformerSelector func(transformers.Route) transformers.Transformer

func selectOutputTransformer(opts ShowJSONOpts, selectTransformer transformerSelector) transformers.Transformer {
	// Explicit data formats operate on the original API response. Auto and text
	// use the default transformation before readable presentation.
	// Missing or non-success routing metadata also uses identity, including errors.
	format := strings.ToLower(opts.Format)
	if opts.ExplicitFormat && format != "auto" && format != "text" || opts.Transform != "" || opts.RawOutput || opts.Operation == "" {
		return transformers.Identity
	}
	switch opts.OutputKind {
	case OutputResponse, OutputPageItem, OutputStreamEvent:
		return selectTransformer(transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind})
	default:
		return transformers.Identity
	}
}

// Feature presentation shares the raw JSON carrier and fixed stream error type.
type outputJSON = clioutput.JSON
type streamResultError = clioutput.StreamResultError
