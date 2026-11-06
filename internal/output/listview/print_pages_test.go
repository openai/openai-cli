package listview

import (
	"errors"
	"io"
	"testing"

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
