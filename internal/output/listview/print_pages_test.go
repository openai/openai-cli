package listview

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/output/transform"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrintedListRetainsPageAndOutputFailures(t *testing.T) {
	upstream := errors.New("synthetic pagination failure")
	sink := errors.New("synthetic output failure")
	reader, writer := io.Pipe()
	require.NoError(t, reader.CloseWithError(sink))
	t.Cleanup(func() { _ = writer.Close() })
	opts := Options{
		Context: t.Context(), Stdout: writer, PrintOnly: true,
		RenderTable: func([]gjson.Result, int) (string, bool, error) { return "data\n", true, nil },
	}
	fetches := 0
	err := printPages(opts, 80, func() (Page, error) {
		fetches++
		return Page{Items: []gjson.Result{gjson.Parse("1")}, More: true}, upstream
	})
	require.ErrorIs(t, err, upstream)
	require.ErrorIs(t, err, sink)
	require.Equal(t, 1, fetches)
}

func TestPrintedPagesPreserveRowsBeforeFetchingOrReportingFailure(t *testing.T) {
	var out strings.Builder
	upstream := errors.New("synthetic page failure")
	opts := Options{Context: t.Context(), Stdout: &out, PrintOnly: true,
		ProjectPrintTable: func(items []gjson.Result) (transform.ListTableProjection, error) {
			return transform.ListTableProjection{Headers: []string{"ID", "NAME"}, Rows: [][]string{{items[0].Str, "sample"}}}, nil
		},
		RenderRecordLabels: func(item gjson.Result, _ bool) (string, error) { return "ID: " + item.Str + "\n", nil },
	}
	ids := []string{"short", "longer-second-id", strings.Repeat("x", 65)}
	calls := 0
	err := printPages(opts, 80, func() (Page, error) {
		if calls > 0 {
			require.Equal(t, 1, strings.Count(out.String(), ids[calls-1]), "previous page must reach stdout before the next request")
		}
		item := gjson.Result{Type: gjson.String, Str: ids[calls]}
		calls++
		if calls == len(ids) {
			return Page{Items: []gjson.Result{item}}, upstream
		}
		return Page{Items: []gjson.Result{item}, More: true}, nil
	})
	require.ErrorIs(t, err, upstream)
	require.Equal(t, len(ids), calls)
	text := out.String()
	require.Equal(t, 1, strings.Count(text, "NAME"))
	require.Contains(t, text, "ID: "+ids[2])
	for _, id := range ids {
		require.Equal(t, 1, strings.Count(text, id))
	}
}
