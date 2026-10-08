package custom

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/tidwall/gjson"
)

// Models use the SDK's single-response Page, whose GetNextPage always returns
// nil. Check that concrete pager before collecting its already-loaded items.
// Cursor pagers never enter this path, even with misleading operation metadata.
func showModelsListViewer[T any](source jsonview.Iterator[T], iter *outputIterator[T], opts ShowJSONOpts) (bool, error) {
	_, singlePage := source.(*pagination.PageAutoPager[T])
	_, selectedModels := any(source).(*selectedModelsIterator)
	if !singlePage && !selectedModels {
		return false, nil
	}
	if opts.Operation != "(resource) models > (method) list" || opts.OutputKind != OutputPageItem ||
		opts.Format != "" && !strings.EqualFold(opts.Format, "auto") || opts.Transform != "" || opts.RawOutput ||
		os.Getenv("CI") != "" || os.Getenv("TERM") == "dumb" || !isTerminal(opts.Stdout) || !term.IsTerminal(os.Stdin.Fd()) {
		return false, nil
	}
	width, height, err := term.GetSize(opts.Stdout.(*os.File).Fd())
	if err != nil || width <= 0 || height <= 0 {
		return false, nil
	}
	if opts.Stdout == os.Stdout {
		return true, streamToStdout(func(*os.File) error { return writeModelsList(iter, opts, width, height) })
	}
	return true, writeModelsList(iter, opts, width, height)
}

func writeModelsList[T any](iter *outputIterator[T], opts ShowJSONOpts, width, height int) error {
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
	content, err := renderListNavigationPage(opts, items, width)
	if err != nil {
		return errors.Join(err, iter.Err())
	}
	if height > 0 && len(items) != 0 && iter.Err() == nil && modelsListNeedsViewport(content, width, height) {
		// This callback serves only the response already loaded by the SDK.
		// Browsing and resizing cannot fetch another API page.
		ctx, cancel := context.WithCancel(opts.Context)
		defer cancel()
		loaded := false
		return runListNavigation(opts, func() (listNavigationPage, error) {
			if loaded || ctx.Err() != nil {
				return listNavigationPage{}, ctx.Err()
			}
			loaded = true
			return listNavigationPage{items: items, more: false}, nil
		}, cancel)
	}
	out := outputWriter{ctx: opts.Context, out: opts.Stdout}
	_, err = io.WriteString(out, content)
	return errors.Join(err, iter.Err())
}

func modelsListNeedsViewport(content string, width, height int) bool {
	if content == "" {
		return false
	}
	// Match the viewer's wrapping and leave one line for the shell prompt.
	wrapped := ansi.Hardwrap(content, max(1, width), true)
	lines := strings.Count(strings.TrimSuffix(wrapped, "\n"), "\n") + 1
	return lines > max(1, height-1)
}
