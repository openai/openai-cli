package jsonview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStaticDisplayUsesLiteralObjectKeys(t *testing.T) {
	t.Parallel()

	result := gjson.Parse(`{"a.b":"literal-value","a":{"b":"nested-value"}}`)
	out := formatJSON(result, 80)

	require.Contains(t, out, "literal-value")
	require.Contains(t, out, "nested-value")
}

func TestExploreTableUsesLiteralObjectKeys(t *testing.T) {
	t.Parallel()

	t.Run("ObjectView", func(t *testing.T) {
		t.Parallel()

		result := gjson.Parse(`{"a.b":"literal-value","a":{"b":"nested-value"}}`)
		view, err := newTableView("", result, false)
		require.NoError(t, err)

		rows := view.table.Rows()
		require.Len(t, rows, 2)
		require.Equal(t, "a.b", rows[0][0])
		require.Equal(t, "literal-value", rows[0][1])
		require.Equal(t, "a", rows[1][0])
	})

	t.Run("ArrayOfObjectsView", func(t *testing.T) {
		t.Parallel()

		result := gjson.Parse(`[{"a.b":"literal-value","a":{"b":"nested-value"}}]`)
		view, err := newTableView("", result, false)
		require.NoError(t, err)

		columns := view.table.Columns()
		rows := view.table.Rows()

		require.Len(t, columns, 2)
		require.Equal(t, "a.b", columns[0].Title)
		require.Equal(t, "a", columns[1].Title)

		require.Len(t, rows, 1)
		require.Equal(t, "literal-value", rows[0][0])
	})

	t.Run("RowLoadedLater", func(t *testing.T) {
		t.Parallel()

		view, err := newTableView("", gjson.Parse(`[{"a.b":"first","a":{"b":"nested-first"}}]`), false)
		require.NoError(t, err)

		view.appendItem(gjson.Parse(`{"a.b":"second","a":{"b":"nested-second"}}`), false)

		column := requireColumn(t, view, "a.b")
		rows := view.table.Rows()
		require.Len(t, rows, 2)
		require.Equal(t, "first", rows[0][column])
		require.Equal(t, "second", rows[1][column])
	})
}

func TestLazyLoadedRowsUseLiteralKeys(t *testing.T) {
	t.Parallel()

	view, err := newTableView("", gjson.Parse(`[{"a.b":"first","a":{"b":"nested-first"}}]`), false)
	require.NoError(t, err)
	view.Resize(80, 24)
	view.iterator = &literalKeyIterator{items: []any{
		map[string]any{
			"a.b": "second",
			"a":   map[string]any{"b": "nested-second"},
		},
	}}

	viewer := &JSONViewer{
		stack: []JSONView{view},
		root:  "test",
	}

	cmd := view.loadMoreData()
	require.NotNil(t, cmd)
	msg := cmd()
	_, _ = viewer.Update(msg)

	column := requireColumn(t, view, "a.b")
	rows := view.table.Rows()
	require.Len(t, rows, 2)
	require.Equal(t, "first", rows[0][column])
	require.Equal(t, "second", rows[1][column])
}

func TestExploreFormatObjectUsesLiteralObjectKeys(t *testing.T) {
	t.Parallel()

	result := gjson.Parse(`{"a.b":"literal-value","a":{"b":"nested-value"}}`)
	got := formatValue(result, false)

	require.Contains(t, got, `a.b:"literal-value"`)
}

func requireColumn(t *testing.T, view *TableView, title string) int {
	t.Helper()

	for i, column := range view.table.Columns() {
		if column.Title == title {
			return i
		}
	}
	t.Fatalf("missing %q column", title)
	return -1
}

type literalKeyIterator struct {
	items []any
	index int
}

func (it *literalKeyIterator) Next() bool {
	if it.index >= len(it.items) {
		return false
	}
	it.index++
	return true
}

func (it *literalKeyIterator) Err() error { return nil }

func (it *literalKeyIterator) Current() any {
	return it.items[it.index-1]
}
