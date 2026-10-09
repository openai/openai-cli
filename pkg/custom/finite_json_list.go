package custom

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/openai/openai-cli/internal/jsonview"
	"github.com/openai/openai-cli/pkg/transformers"
	"github.com/tidwall/pretty"
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
	iter := &outputIterator[T]{
		source: &finiteJSONSource[T]{source: source}, context: opts.Context,
		transform: selectOutputTransformer(opts, selectTransformer),
		route:     transformers.Route{Operation: opts.Operation, OutputKind: opts.OutputKind}, remaining: maximum,
	}
	list := &finiteJSONList{iter: iter, opts: opts}
	if file, ok := opts.Stdout.(*os.File); ok && file == os.Stdout {
		if isTerminal(file) {
			width, height, err := term.GetSize(file.Fd())
			if err != nil {
				width, height = 100, 40
			}
			return true, list.showTerminal(width, height)
		}
		// Preserve lone consumer EPIPE success without hiding joined failures.
		return true, streamToStdout(func(out *os.File) error { return list.write(out) })
	}
	return true, list.write(opts.Stdout)
}

// Validate RawJSON before outputIterator's permissive parser can discard a
// malformed suffix. Ordinary Go values still use outputIterator's JSON encoder.
type finiteJSONSource[T any] struct {
	source jsonview.Iterator[T]
	err    error
}

func (it *finiteJSONSource[T]) Next() bool {
	if it.err != nil || !it.source.Next() {
		return false
	}
	if raw, ok := any(it.source.Current()).(hasRawJSON); ok && !json.Valid([]byte(raw.RawJSON())) {
		it.err = errors.New("invalid JSON list item")
		return false
	}
	return true
}
func (it *finiteJSONSource[T]) Current() T { return it.source.Current() }
func (it *finiteJSONSource[T]) Err() error { return errors.Join(it.err, it.source.Err()) }

// The same framing state continues across a terminal's bounded prefix and pager.
// Pipes and files never collect that prefix.
type finiteJSONList struct {
	iter          jsonview.Iterator[outputJSON]
	opts          ShowJSONOpts
	started, done bool
}

func (list *finiteJSONList) next() ([]byte, error) {
	if list.done {
		return nil, nil
	}
	if !list.iter.Next() {
		if err := list.err(nil); err != nil {
			return nil, err
		}
		list.done = true
		if !list.started {
			return []byte("[]\n"), nil
		}
		return []byte("\n]\n"), nil
	}
	var item bytes.Buffer
	item.WriteString("  ")
	// Indent validates transformed values and retains numeric spelling. Only the
	// current item is formatted; its separator is not emitted before validation.
	if err := json.Indent(&item, []byte(list.iter.Current().Raw), "  ", "  "); err != nil {
		return nil, list.err(errors.New("invalid JSON list item"))
	}
	data := item.Bytes()
	if shouldUseColors(list.opts.Stdout) {
		data = pretty.Color(data, pretty.TerminalStyle)
	}
	separator := "[\n"
	if list.started {
		separator = ",\n"
	}
	list.started = true
	return append([]byte(separator), data...), nil
}

// A cancellation arriving during a failed write must survive EPIPE suppression.
func (list *finiteJSONList) err(err error) error {
	return errors.Join(err, list.opts.Context.Err(), list.iter.Err())
}

func (list *finiteJSONList) write(out io.Writer) error {
	for {
		data, err := list.next()
		if err != nil || data == nil {
			return list.err(err)
		}
		if _, err := (outputWriter{ctx: list.opts.Context, out: out}).Write(data); err != nil {
			return list.err(err)
		}
	}
}

func (list *finiteJSONList) showTerminal(width, height int) error {
	var prefix bytes.Buffer
	lines := 0
	for lines < max(1, height-3) {
		data, err := list.next()
		if data != nil {
			prefix.Write(data)
			lines += countTerminalLines(data, width)
		}
		if err != nil || data == nil || list.done {
			return streamToStdout(func(out *os.File) error {
				_, writeErr := (outputWriter{ctx: list.opts.Context, out: out}).Write(prefix.Bytes())
				return list.err(errors.Join(err, writeErr))
			})
		}
	}
	// Keep existing long-output paging. The prefix holds at most one screen plus
	// the item crossing that screen, independent of the total list length.
	return streamOutput(list.opts.Title, func(out *os.File) error {
		if _, err := (outputWriter{ctx: list.opts.Context, out: out}).Write(prefix.Bytes()); err != nil {
			return list.err(err)
		}
		return list.write(out)
	})
}
