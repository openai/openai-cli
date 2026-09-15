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
}

func TestExploreFormatObjectUsesLiteralObjectKeys(t *testing.T) {
	t.Parallel()

	result := gjson.Parse(`{"a.b":"literal-value","a":{"b":"nested-value"}}`)
	got := formatValue(result, false)

	require.Contains(t, got, `a.b:"literal-value"`)
}
