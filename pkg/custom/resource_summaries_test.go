package custom

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResourceSummaryNoticePreservesFinalErrors(t *testing.T) {
	const hint = "Summary; use --format json for full data."
	item := outputJSON{gjson.Parse(`{"id":"file_summary","object":"file","created_at":17}`)}
	upstreamErr := errors.New("synthetic page failure")
	writeErr := errors.New("synthetic notice failure")
	for _, tc := range []struct {
		name string
		err  error
	}{{"notice write failure", writeErr}, {"short notice write", nil}, {"closed notice pipe", syscall.EPIPE}} {
		t.Run(tc.name, func(t *testing.T) {
			source := &transformTestIterator{items: []any{item, item}, err: upstreamErr}
			var output bytes.Buffer
			writer := resourceSummaryTestWriter(func(p []byte) (int, error) {
				if strings.Contains(string(p), hint) {
					require.Equal(t, 3, source.calls, "notice must follow the whole list")
					return 0, tc.err
				}
				return output.Write(p)
			})
			err := ShowJSONIterator(source, -1, ShowJSONOpts{
				Operation: "(resource) files > (method) list", OutputKind: OutputPageItem, Stdout: writer,
			})
			want := tc.err
			if want == nil {
				want = io.ErrShortWrite
			}
			if errors.Is(tc.err, syscall.EPIPE) {
				require.NotErrorIs(t, err, syscall.EPIPE, "a broken hint pipe must not make the page error suppressible")
				require.False(t, isOutputBrokenPipe(err))
			} else {
				require.ErrorIs(t, err, want)
			}
			require.ErrorIs(t, err, upstreamErr)
			require.Equal(t, 2, strings.Count(output.String(), "ID: file_summary\n"))
		})
	}

	for _, pageErr := range []error{nil, upstreamErr} {
		name := "canceled after records"
		if pageErr != nil {
			name = "canceled after failed page"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := &resourceSummaryEndIterator{transformTestIterator: transformTestIterator{items: []any{item, item}, err: pageErr}, finish: cancel}
			var output bytes.Buffer
			err := ShowJSONIterator(source, -1, ShowJSONOpts{
				Context: ctx, Operation: "(resource) files > (method) list", OutputKind: OutputPageItem, Stdout: &output,
			})
			require.ErrorIs(t, err, context.Canceled)
			if pageErr != nil {
				require.ErrorIs(t, err, pageErr)
			}
			require.Equal(t, 2, strings.Count(output.String(), "ID: file_summary\n"))
			require.NotContains(t, output.String(), hint)
		})
	}
}

type resourceSummaryTestWriter func([]byte) (int, error)

func (write resourceSummaryTestWriter) Write(p []byte) (int, error) { return write(p) }

type resourceSummaryEndIterator struct {
	transformTestIterator
	finish func()
}

func (it *resourceSummaryEndIterator) Next() bool {
	more := it.transformTestIterator.Next()
	if !more {
		it.finish()
	}
	return more
}
