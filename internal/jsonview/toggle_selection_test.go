package jsonview

import (
	"encoding/json"
	"fmt"
	"testing"

	"charm.land/bubbles/v2/help"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExplorerToggleRawPreservesSelection(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`["first","second","third"]`,
		`[{"id":"first"},{"id":"second"},{"id":"third"}]`,
		`{"first":1,"second":2,"third":3}`,
	} {
		t.Run(input, func(t *testing.T) {
			view, err := newTableView("", gjson.Parse(input), false)
			require.NoError(t, err)
			viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
			viewer.resize(80, 24)
			view.table.SetCursor(2)
			selected := viewer.getSelectedContent()
			for i := 0; i < 4; i++ {
				explorerKey(viewer, "r")
				require.Equal(t, 2, viewer.current().(*TableView).table.Cursor())
				require.Equal(t, selected, viewer.getSelectedContent(), "printing after a toggle must return the selected item")
			}
		})
	}
}

func TestExplorerToggleRawPreservesParentSelection(t *testing.T) {
	t.Parallel()
	view, err := newTableView("", gjson.Parse(`[{"id":"first"},{"id":"second"}]`), false)
	require.NoError(t, err)
	viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
	viewer.resize(80, 24)
	view.table.SetCursor(1)
	explorerKey(viewer, "l")
	require.Len(t, viewer.stack, 2)
	explorerKey(viewer, "r")
	explorerKey(viewer, "h")
	require.Equal(t, `{"id":"second"}`, viewer.getSelectedContent())
}

func TestExplorerToggleRawKeepsScrolledSelectionVisible(t *testing.T) {
	t.Parallel()
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf("item-%02d", i)
	}
	data, err := json.Marshal(items)
	require.NoError(t, err)
	for _, height := range []int{12, 4, 7} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			view, err := newTableView("", gjson.ParseBytes(data), false)
			require.NoError(t, err)
			viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
			viewer.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
			view.table.MoveDown(25)
			require.Contains(t, viewer.current().View(), "item-25")
			for i := 0; i < 4; i++ {
				viewer.Update(tea.WindowSizeMsg{Width: 80, Height: height})
				explorerKey(viewer, "r")
				require.Equal(t, "item-25", viewer.getSelectedContent())
				viewer.Update(tea.WindowSizeMsg{Width: 80, Height: 12})
				require.Equal(t, 25, viewer.current().(*TableView).table.Cursor())
				require.Equal(t, "item-25", viewer.getSelectedContent())
				require.Contains(t, viewer.current().View(), "item-25", "growing the terminal must reveal the saved selection")
			}
		})
	}
}

func TestExplorerToggleRawInTinyTerminal(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`["first","second","third"]`,
		`[{"id":"first"},{"id":"second"},{"id":"third"}]`,
		`{"first":1,"second":2,"third":3}`,
	} {
		for _, height := range []int{4, 7} {
			t.Run(fmt.Sprintf("%s/height=%d", input, height), func(t *testing.T) {
				view, err := newTableView("", gjson.Parse(input), false)
				require.NoError(t, err)
				viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
				viewer.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				view.table.SetCursor(2)
				selected := viewer.getSelectedContent()

				for i := 0; i < 4; i++ {
					viewer.Update(tea.WindowSizeMsg{Width: 80, Height: height})
					require.LessOrEqual(t, viewer.current().(*TableView).table.Height(), 0)
					require.NotPanics(t, func() {
						explorerKey(viewer, "r")
						viewer.View()
						viewer.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
						viewer.View()
					})
					require.Equal(t, 2, viewer.current().(*TableView).table.Cursor())
					require.Equal(t, selected, viewer.getSelectedContent())
					require.Contains(t, viewer.current().View(), "third")
				}
			})
		}
	}
}
