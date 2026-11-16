package app

import (
	"errors"
	"strings"

	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
)

// Only finite-item boundaries produce arrays. Extraction and raw output retain
// their record contracts, even when their selected format is JSON.
func showFiniteJSONList[T any](source jsonview.Iterator[T], maximum int64, opts ShowJSONOpts, selectTransformer transformerSelector) (bool, error) {
	if opts.OutputKind != OutputPageItem || !strings.EqualFold(opts.Format, "json") || opts.Transform != "" || opts.RawOutput {
		return false, nil
	}
	if err := errors.Join(opts.Context.Err(), source.Err()); err != nil {
		return true, err
	}
	if maximum != 0 {
		if handled, err := showModelsListSelection(source, maximum, opts); handled {
			return true, err
		}
	}
	return true, clioutput.WriteFiniteJSONList(source, clioutput.FiniteJSONListOptions{
		IteratorOptions: clioutput.IteratorOptions{
			Context:   opts.Context,
			Transform: selectOutputTransformer(opts, selectTransformer),
			Route:     transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind},
			Limit:     maximum,
		},
		Stdout:   opts.Stdout,
		Terminal: isTerminal(opts.Stdout),
		Title:    opts.Title,
	})
}
