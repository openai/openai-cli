package jsonview

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type explorerIterator struct {
	items []any
	index int
}

func (it *explorerIterator) Next() bool   { it.index++; return it.index <= len(it.items) }
func (it *explorerIterator) Current() any { return it.items[it.index-1] }
func (it *explorerIterator) Err() error   { return nil }

func explorerKey(v *JSONViewer, key string) tea.Cmd {
	_, cmd := v.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return cmd
}

func TestExplorerToggleRetainsLoadedItems(t *testing.T) {
	view, err := newTableView("", gjson.Parse(`[{"id":"first"}]`), false)
	require.NoError(t, err)
	it := &explorerIterator{items: []any{json.RawMessage(`{"id":"second"}`), json.RawMessage(`{"id":"third"}`)}}
	view.iterator = it
	v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New()}
	v.resize(80, 24)
	cmd := explorerKey(v, "j")
	require.NotNil(t, cmd)
	v.Update(cmd())
	require.Len(t, v.current().(*TableView).rowData, 2)
	for i := 0; i < 4; i++ {
		explorerKey(v, "r")
		tv := v.current().(*TableView)
		require.Len(t, tv.rowData, 2, "toggle discarded loaded data")
		require.Equal(t, "first", tv.rowData[0].Get("id").Str)
		require.Equal(t, "second", tv.rowData[1].Get("id").Str)
	}
	cmd = explorerKey(v, "j")
	require.NotNil(t, cmd)
	v.Update(cmd())
	explorerKey(v, "r")
	tv := v.current().(*TableView)
	require.Len(t, tv.rowData, 3)
	require.Equal(t, "third", tv.rowData[2].Get("id").Str)
	require.Equal(t, 2, it.index)
	tv.table.SetCursor(2)
	cmd = explorerKey(v, "j")
	require.NotNil(t, cmd)
	v.Update(cmd())
	explorerKey(v, "r")
	require.Len(t, v.current().(*TableView).rowData, 3)
	require.Equal(t, 3, it.index)
}

type blockedExplorerIterator struct {
	started chan struct{}
	release chan struct{}
}

func (it *blockedExplorerIterator) Next() bool {
	close(it.started)
	<-it.release
	return true
}
func (it *blockedExplorerIterator) Current() any {
	return json.RawMessage(`{"id":"second","value":"\u001b]52;c;sample\u0007"}`)
}
func (it *blockedExplorerIterator) Err() error { return nil }

func TestExplorerToggleDuringLoad(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested=%v", nested), func(t *testing.T) {
			view, err := newTableView("", gjson.Parse(`[{"id":"first","value":"initial"}]`), false)
			require.NoError(t, err)
			it := &blockedExplorerIterator{started: make(chan struct{}), release: make(chan struct{})}
			view.iterator = it
			v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New()}
			v.resize(80, 24)
			cmd := explorerKey(v, "j")
			require.NotNil(t, cmd)
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()
			<-it.started
			// Ensure the blocked command is released even if an assertion fails.
			defer close(it.release)
			for i := 0; i < 3; i++ {
				explorerKey(v, "r")
				require.Nil(t, explorerKey(v, "j"), "must not schedule a second iterator read")
				v.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
				_ = v.View()
			}
			if nested {
				explorerKey(v, "l")
				require.Len(t, v.stack, 2)
			}
			// Unblock without closing twice in the deferred cleanup.
			it.release <- struct{}{}
			msg := <-done
			v.Update(msg)
			if nested {
				require.Len(t, v.stack, 2)
				explorerKey(v, "h")
			}
			tv := v.current().(*TableView)
			require.Len(t, tv.rowData, 2)
			require.False(t, tv.isLoading)
			require.Equal(t, `"second"`, tv.table.Rows()[1][0])
			requireNoRawTerminalControls(t, tv.table.Rows()[1][1])
			explorerKey(v, "r")
			require.Equal(t, "second", tv.table.Rows()[1][0])
			tv.table.SetCursor(1)
			explorerKey(v, "l")
			require.Equal(t, "[1]", v.current().GetPath())
			require.Equal(t, "second", v.current().GetData().Get("id").Str)
			explorerKey(v, "r")
			explorerKey(v, "h")
			require.Len(t, v.current().(*TableView).rowData, 2)
		})
	}
}

func TestExplorerToggleNonstreaming(t *testing.T) {
	for _, input := range []string{`[1,"two",null]`, `[{"id":"one"},{"id":"two"}]`, `{"id":"one","value":[1,2]}`, `"line one\nline two"`} {
		t.Run(input, func(t *testing.T) {
			view, err := newView("", gjson.Parse(input), false)
			require.NoError(t, err)
			v := &JSONViewer{stack: []JSONView{view}, root: "test", help: help.New()}
			v.resize(80, 24)
			before := v.View()
			explorerKey(v, "r")
			require.Equal(t, input, v.current().GetData().Raw)
			explorerKey(v, "r")
			require.Equal(t, before, v.View())
		})
	}
}
