package models

import (
	"context"
	"errors"
	"io"

	clioutput "github.com/openai/openai-cli/internal/output"
	"github.com/openai/openai-cli/internal/output/listview"
	"github.com/tidwall/gjson"
)

// WriteList renders the already loaded response or opens navigation when it overflows.
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
	content, err := opts.RenderPage(items, width)
	if err != nil {
		return errors.Join(err, iter.Err())
	}
	if height > 0 && iter.Err() == nil && NeedsViewport(content, width, height) {
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
	out := clioutput.Writer{Context: opts.Context, Sink: opts.Stdout}
	_, err = io.WriteString(out, content)
	return errors.Join(err, iter.Err())
}

// NeedsViewport preserves display-cell widths and space for the shell prompt.
func NeedsViewport(content string, width, height int) bool {
	if content == "" {
		return false
	}
	// Use the same tab stops, display-cell widths, and prompt-row reservation
	// as the cursor-page lists, including malformed-record labeled fallback.
	return !listview.ContentFits(content, width, height)
}
