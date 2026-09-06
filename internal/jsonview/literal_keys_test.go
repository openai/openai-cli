package jsonview

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const literalKeyFixture = `{"a.b":"literal-value","a":{"b":"nested-value"}}`

func TestObjectTableUsesLiteralKeys(t *testing.T) {
	t.Parallel()

	view := newObjectTableView("", gjson.Parse(literalKeyFixture), false)
	rows := view.table.Rows()

	for _, row := range rows {
		if row[0] == "a.b" {
			require.Equal(t, "literal-value", row[1])
			return
		}
	}
	t.Fatal("missing literal a.b row")
}

func TestArrayOfObjectsTableUsesLiteralKeys(t *testing.T) {
	t.Parallel()

	view, err := newTableView("", gjson.Parse(`[`+literalKeyFixture+`]`), false)
	require.NoError(t, err)

	column := requireColumn(t, view, "a.b")
	require.Equal(t, "literal-value", view.table.Rows()[0][column])
}

func TestObjectPreviewUsesLiteralKeys(t *testing.T) {
	t.Parallel()

	preview := formatValue(gjson.Parse(literalKeyFixture), false)
	require.Contains(t, preview, `a.b:"literal-value"`)
	require.NotContains(t, preview, `a.b:"nested-value"`)
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

	msg := view.loadMoreData(false)()
	require.Nil(t, msg)

	column := requireColumn(t, view, "a.b")
	rows := view.table.Rows()
	require.Len(t, rows, 2)
	require.Equal(t, "second", rows[1][column])
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
