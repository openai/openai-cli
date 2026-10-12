package custom

import (
	"context"

	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/tidwall/gjson"
)

// Selection uses only the SDK's already-loaded models response. Cursor pagers
// must never enter a path that collects all their pages before displaying data.
func showModelsListSelection[T any](source jsonview.Iterator[T], maximum int64, opts ShowJSONOpts) (bool, error) {
	options, enabled := opts.Context.Value(modelsListOptionsKey{}).(modelsListOptions)
	if !enabled || options.applied || opts.Operation != "(resource) models > (method) list" || opts.OutputKind != OutputPageItem {
		return false, nil
	}
	if _, singlePage := source.(*pagination.PageAutoPager[T]); !singlePage {
		return true, &modelsListError{"Model selection requires a single models response."}
	}
	iter := &outputIterator[T]{source: source, context: opts.Context, transform: transformers.Identity, remaining: -1}
	var items []gjson.Result
	for iter.Next() {
		items = append(items, iter.Current().Result)
	}
	if err := iter.Err(); err != nil {
		return true, err
	}
	options.selection.Limit = maximum
	selected, supported, err := transformers.SelectModels(opts.Context, items, options.selection)
	if err != nil {
		return true, err
	}
	if !supported {
		return true, &modelsListError{"Cannot select model records with missing, invalid, or ambiguous IDs."}
	}
	options.applied = true
	opts.Context = context.WithValue(opts.Context, modelsListOptionsKey{}, options)
	return true, ShowJSONIterator(&selectedModelsIterator{items: selected}, -1, opts)
}

// This private iterator can only contain selected records from one response.
// The normal formatters and terminal viewer retain ownership of presentation.
type selectedModelsIterator struct {
	items []gjson.Result
	index int
}

func (it *selectedModelsIterator) Next() bool {
	if it.index >= len(it.items) {
		return false
	}
	it.index++
	return true
}

func (it *selectedModelsIterator) Current() outputJSON { return outputJSON{it.items[it.index-1]} }
func (it *selectedModelsIterator) Err() error          { return nil }
