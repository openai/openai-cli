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
	ctx   context.Context
	items []gjson.Result
	size  int
}

func localModelPages(ctx context.Context, items []gjson.Result) (modelPages, error) {
	pages := modelPages{ctx: ctx, items: items, size: len(items)}
	if len(items) <= modelListPageSize {
		return pages, ctx.Err()
	}
	options, _ := ctx.Value(modelsListOptionsKey{}).(modelsListOptions)
	if !options.applied {
		// Sort across page boundaries. If any identity is ambiguous, the whole
		// response must retain its existing labeled fallback and input order.
		sorted, supported, err := transformers.SelectModels(ctx, items, transformers.ModelSelection{
			Descending: options.selection.Descending, Limit: -1,
		})
		if err != nil || !supported {
			return pages, err
		}
		pages.items = sorted
	}
	pages.size = modelListPageSize
	return pages, ctx.Err()
}

// The SDK has already loaded the response. Navigation only consumes this slice.
func (p *modelPages) next() (listview.Page, error) {
	if err := p.ctx.Err(); err != nil {
		return listview.Page{}, err
	}
	count := min(p.size, len(p.items))
	page := listview.Page{Items: p.items[:count], More: count < len(p.items), Local: true}
	p.items = p.items[count:]
	return page, nil
}
