package models

import (
	"context"
	"errors"
	"io"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/tidwall/gjson"
)

// WriteList opens the pager for loaded terminal records, including short and empty results.
func WriteList[T any](iter *clioutput.Iterator[T], opts listview.Options, width, height int) error {
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
		// This callback serves only the response already loaded by the SDK.
		// Browsing and resizing cannot fetch another API page.
		ctx, cancel := context.WithCancel(opts.Context)
		defer cancel()
		loaded := false
		return listview.Run(opts, func() (listview.Page, error) {
			if loaded || ctx.Err() != nil {
				return listview.Page{}, ctx.Err()
			}
			loaded = true
			return listview.Page{Items: items, More: false}, nil
		}, cancel)
	}
	content, err := opts.RenderPage(items, width)
	if err != nil {
		return errors.Join(err, iter.Err())
	}
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	_, err = io.WriteString(out, content)
	return errors.Join(err, iter.Err())
}
