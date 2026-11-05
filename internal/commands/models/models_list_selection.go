package models

import (
	"context"

	"github.com/openai/openai-cli/internal/jsonview"
	clioutput "github.com/openai/openai-cli/internal/output"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/tidwall/gjson"
)

// Select uses only the SDK's already-loaded models response. Cursor pagers
// must never enter a path that collects all their pages before displaying data.
func Select[T any](ctx context.Context, source jsonview.Iterator[T], maximum int64) (context.Context, jsonview.Iterator[clioutput.JSON], bool, error) {
	options, enabled := ctx.Value(modelsListOptionsKey{}).(modelsListOptions)
	if !enabled || options.applied {
		return ctx, nil, false, nil
	}
	if _, singlePage := source.(*pagination.PageAutoPager[T]); !singlePage {
		return ctx, nil, true, &ListError{"Model selection requires a single models response."}
	}
	iter := clioutput.NewIterator(source, clioutput.IteratorOptions{
		Context:   ctx,
		Transform: transformers.Identity,
		Limit:     -1,
	})
	var items []gjson.Result
	for iter.Next() {
		items = append(items, iter.Current().Result)
	}
	if err := iter.Err(); err != nil {
		return ctx, nil, true, err
	}
	options.selection.Limit = maximum
	selected, supported, err := transformers.SelectModels(ctx, items, options.selection)
	if err != nil {
		return ctx, nil, true, err
	}
	if !supported {
		return ctx, nil, true, &ListError{"Cannot select model records with missing, invalid, or ambiguous IDs."}
	}
	options.applied = true
	ctx = context.WithValue(ctx, modelsListOptionsKey{}, options)
	return ctx, &selectedModelsIterator{items: selected}, true, nil
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

func (it *selectedModelsIterator) Current() clioutput.JSON {
	return clioutput.JSON{Result: it.items[it.index-1]}
}
func (it *selectedModelsIterator) Err() error { return nil }

// IsListSource admits only the SDK single-response pager or selected records.
// Cursor pagers must remain lazy even if operation metadata says models.
func IsListSource[T any](source jsonview.Iterator[T]) bool {
	_, singlePage := source.(*pagination.PageAutoPager[T])
	_, selected := any(source).(*selectedModelsIterator)
	return singlePage || selected
}
