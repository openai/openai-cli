package models

import (
	"context"
	"errors"
	"io"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	transformers "github.com/openai/openai-cli/internal/output/transform"
	"github.com/tidwall/gjson"
)

// Local pages bound terminal rendering without limiting the number of displayed models.
const modelListPageSize = 100

// WriteList opens the pager for loaded terminal records, including short and empty results.
func WriteList[T any](iter *clioutput.Iterator[T], opts listview.Options, width, height int, diagnostics bool) error {
	var items []gjson.Result
	for iter.Next() {
		items = append(items, iter.Current().Result)
	}
	if len(items) == 0 && iter.Err() != nil {
		return iter.Err()
	}
	if err := opts.Context.Err(); err != nil {
		return errors.Join(err, iter.Err())
	}
	if height > 0 && iter.Err() == nil {
		ctx, cancel := context.WithCancel(opts.Context)
		defer cancel()
		pages, err := localModelPages(ctx, items)
		if err != nil {
			return err
		}
		if pages.size < len(items) {
			total := len(items)
			opts.RenderPage = func(page []gjson.Result, width int) (string, error) {
				// localModelPages checked every model identity before splitting.
				content, _, err := renderTable(ctx, page, width, diagnostics, opts.Style, total)
				return content, err
			}
		}
		return listview.Run(opts, pages.next, cancel)
	}
	content, err := opts.RenderPage(items, width)
	if err != nil {
		return errors.Join(err, iter.Err())
	}
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	_, err = io.WriteString(out, content)
	return errors.Join(err, iter.Err())
}

type modelPages struct {
	ctx        context.Context
	items      []gjson.Result
	size       int
	total      int
	fullLabels bool
}

func localModelPages(ctx context.Context, items []gjson.Result) (modelPages, error) {
	pages := modelPages{ctx: ctx, items: items, size: min(modelListPageSize, len(items)), total: len(items)}
	options, _ := ctx.Value(modelsListOptionsKey{}).(modelsListOptions)
	if !options.applied {
		sorted, supported, err := sortModelRecords(ctx, items)
		if err != nil {
			return pages, err
		}
		pages.items = sorted
		if !supported {
			pages.size, pages.fullLabels = len(items), true
		}
	}
	return pages, ctx.Err()
}

// The SDK has already loaded the response. Navigation only consumes this slice.
func (p *modelPages) next() (listview.Page, error) {
	if err := p.ctx.Err(); err != nil {
		return listview.Page{}, err
	}
	count := min(p.size, len(p.items))
	page := listview.Page{Items: p.items[:count], More: count < len(p.items), Local: true, Total: p.total, FullLabels: p.fullLabels}
	p.items = p.items[count:]
	return page, nil
}

// Sort once before the viewer selects a screen. Reordering overlapping screens
// independently could show an ID twice and skip another when moving one row.
func sortModelRecords(ctx context.Context, items []gjson.Result) ([]gjson.Result, bool, error) {
	options, _ := ctx.Value(modelsListOptionsKey{}).(modelsListOptions)
	sorted, supported, err := transformers.SelectModels(ctx, items, transformers.ModelSelection{
		Descending: options.selection.Descending, Limit: -1,
	})
	if err != nil || !supported {
		return items, false, err
	}
	return sorted, true, nil
}
