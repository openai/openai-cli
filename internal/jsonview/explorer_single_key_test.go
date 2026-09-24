package jsonview

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// A streamed array whose items all share a single key keeps a one-column table.
// Rows appended by the lazy loader must go through the same column mapping as the
// preloaded rows, otherwise the table silently changes shape mid-scroll.
func TestExplorerLazyLoadSingleKeyRowsStayColumnShaped(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw=%v", raw), func(t *testing.T) {
			view, err := newTableView("", gjson.Parse(`[{"id":"first"}]`), raw)
			require.NoError(t, err)
			require.Len(t, view.columns, 1)
			require.Equal(t, "id", view.columns[0].Title)

			view.iterator = &explorerIterator{items: []any{json.RawMessage(`{"id":"second"}`)}}
			v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New(), rawMode: raw}
			v.resize(80, 24)

			preloaded := view.table.Rows()[0]

			cmd := explorerKey(v, "j")
			require.NotNil(t, cmd)
			v.Update(cmd())

			require.Len(t, view.rowData, 2)
			rows := view.table.Rows()
			require.Len(t, rows, 2)
			require.Len(t, rows[1], len(view.columns), "appended row must match the column count")
			require.Equal(t, len(preloaded), len(rows[1]), "appended row must render like the preloaded rows")
			if raw {
				require.Equal(t, []string{`"second"`}, []string(rows[1]))
			} else {
				require.Equal(t, []string{"second"}, []string(rows[1]))
			}
		})
	}
}

// A column-shaped table keeps its shape when a streamed item carries keys the
// preloaded items never showed.
func TestExplorerLazyLoadSingleKeyIgnoresUnknownKeys(t *testing.T) {
	view, err := newTableView("", gjson.Parse(`[{"id":"first"}]`), false)
	require.NoError(t, err)
	view.iterator = &explorerIterator{items: []any{json.RawMessage(`{"id":"second","extra":"dropped"}`)}}
	v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New()}
	v.resize(80, 24)

	cmd := explorerKey(v, "j")
	require.NotNil(t, cmd)
	v.Update(cmd())

	rows := view.table.Rows()
	require.Len(t, rows, 2)
	require.Equal(t, []string{"second"}, []string(rows[1]))
	require.Equal(t, `{"id":"second","extra":"dropped"}`, view.rowData[1].Raw,
		"the full item must stay reachable even though only known columns render")
}

// A plain (non-object) array keeps the single "Items" column rendering.
func TestExplorerLazyLoadScalarArrayKeepsItemColumn(t *testing.T) {
	view, err := newTableView("", gjson.Parse(`["one"]`), false)
	require.NoError(t, err)
	require.Nil(t, view.columnKeys)
	view.iterator = &explorerIterator{items: []any{json.RawMessage(`"two"`)}}
	v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New()}
	v.resize(80, 24)

	cmd := explorerKey(v, "j")
	require.NotNil(t, cmd)
	v.Update(cmd())

	rows := view.table.Rows()
	require.Len(t, rows, 2)
	require.Equal(t, []string{"two"}, []string(rows[1]))
}
