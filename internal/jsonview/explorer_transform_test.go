package jsonview

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExplorerTransformValues(t *testing.T) {
	for _, value := range []string{`null`, `""`, `0`, `false`, `{}`, `[]`, `"hello"`, `42`, `true`, `{"projection":"must not apply twice","extra":null}`, `[1,"two",null]`} {
		t.Run(value, func(t *testing.T) {
			raw := `{"id":"model-synthetic","projection":` + value + `,"unknown":"keep","explicit_null":null}`
			var sdk openai.Model
			require.NoError(t, json.Unmarshal([]byte(raw), &sdk))
			var ordinary map[string]any
			require.NoError(t, json.Unmarshal([]byte(raw), &ordinary))
			for _, item := range []any{sdk, ordinary} {
				for _, transform := range []string{"projection", "missing", ""} {
					want := value
					if transform != "projection" {
						want = raw
					}
					initial, err := marshalItemsToJSONArray([]any{item, item}, transform)
					require.NoError(t, err)
					require.JSONEq(t, "["+want+","+want+"]", string(initial))
					view, err := newTableView("", gjson.ParseBytes(initial), false)
					require.NoError(t, err)
					it := &explorerIterator{items: []any{item, item}}
					view.iterator, view.transform = it, transform
					viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
					viewer.resize(100, 24)
					for loaded := 2; loaded < 4; loaded++ {
						view.table.SetCursor(loaded - 1)
						command := explorerKey(viewer, "j")
						require.NotNil(t, command)
						message := command().(tableItemMsg)
						require.NoError(t, message.err)
						require.Len(t, view.rowData, loaded, "command must not mutate UI state")
						viewer.Update(message)
						require.Len(t, view.rowData, loaded+1)
						require.Equal(t, loaded-1, it.index, "one read per scheduled load")
					}
					for toggle := 0; toggle < 3; toggle++ {
						explorerKey(viewer, "r")
						require.Same(t, view, viewer.current())
						for index, result := range view.rowData {
							require.JSONEq(t, want, result.Raw)
							view.table.SetCursor(index)
							explorerKey(viewer, "p")
							expected := want
							if result.Type == gjson.String {
								expected = result.Str
							}
							if result.Type == gjson.String {
								require.Equal(t, expected, viewer.message)
							} else {
								require.JSONEq(t, expected, viewer.message)
							}
							if viewer.canNavigateInto(result) {
								explorerKey(viewer, "l")
								require.Equal(t, fmt.Sprintf("[%d]", index), viewer.current().GetPath())
								require.JSONEq(t, want, viewer.current().GetData().Raw)
								explorerKey(viewer, "h")
							}
						}
					}
					require.Equal(t, 2, it.index, "printing, toggling and navigation must not consume items")
				}
			}
		})
	}
}

func TestExplorerTransformPendingLoad(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprint(nested), func(t *testing.T) {
			initial, err := marshalItemsToJSONArray([]any{json.RawMessage(`{"value":{"text":"initial"}}`)}, "value")
			require.NoError(t, err)
			view, err := newTableView("", gjson.ParseBytes(initial), false)
			require.NoError(t, err)
			it := &blockedExplorerIterator{started: make(chan struct{}), release: make(chan struct{})}
			view.iterator, view.transform = it, "value"
			viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
			viewer.resize(100, 24)
			command := explorerKey(viewer, "j")
			require.NotNil(t, command)
			done := make(chan tea.Msg, 1)
			defer close(it.release)
			go func() { done <- command() }()
			<-it.started
			for i := 0; i < 3; i++ {
				explorerKey(viewer, "r")
				require.Same(t, view, viewer.current())
				require.Nil(t, explorerKey(viewer, "j"))
				viewer.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
				_ = viewer.View()
			}
			if nested {
				explorerKey(viewer, "l")
				require.Len(t, viewer.stack, 2)
			}
			it.release <- struct{}{}
			message := <-done
			require.Len(t, view.rowData, 1)
			viewer.Update(message)
			if nested {
				require.Len(t, viewer.stack, 2)
				explorerKey(viewer, "h")
			}
			require.Same(t, view, viewer.current())
			require.False(t, view.isLoading)
			require.Len(t, view.rowData, 2)
			require.Equal(t, `"\u001b]52;c;sample\u0007"`, view.rowData[1].Raw)
			for i := 0; i < 3; i++ {
				explorerKey(viewer, "r")
				requireNoRawTerminalControls(t, view.table.Rows()[1][0])
				view.table.SetCursor(1)
				explorerKey(viewer, "p")
				require.Equal(t, `\u001b]52;c;sample\u0007`, viewer.message)
			}
		})
	}
}

type transformErrorIterator struct {
	currentCalls int
	nextCalls    int
	err          error
}

func (it *transformErrorIterator) Next() bool   { it.nextCalls++; return false }
func (it *transformErrorIterator) Current() any { it.currentCalls++; return nil }
func (it *transformErrorIterator) Err() error   { return it.err }

func TestExplorerTransformErrors(t *testing.T) {
	for _, transform := range []string{"", "projection"} {
		t.Run(transform, func(t *testing.T) {
			_, err := marshalItemsToJSONArray([]any{make(chan int)}, transform)
			var unsupported *json.UnsupportedTypeError
			require.ErrorAs(t, err, &unsupported)
			for _, upstream := range []error{nil, context.Canceled} {
				it := &transformErrorIterator{err: upstream}
				if upstream != nil {
					require.ErrorIs(t, ExploreJSONStream("synthetic", it, transform), upstream)
					require.Equal(t, 1, it.nextCalls)
					require.Zero(t, it.currentCalls)
				}
				view, err := newTableView("", gjson.Parse(`["initial"]`), false)
				require.NoError(t, err)
				view.iterator, view.transform = it, transform
				viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
				viewer.resize(100, 24)
				command := explorerKey(viewer, "j")
				require.NotNil(t, command)
				message := command().(tableItemMsg)
				require.ErrorIs(t, message.err, upstream)
				require.False(t, message.result.Exists())
				viewer.Update(message)
				require.False(t, view.isLoading)
				require.Len(t, view.rowData, 1)
				require.Zero(t, it.currentCalls)
			}
			view, err := newTableView("", gjson.Parse(`["initial"]`), false)
			require.NoError(t, err)
			it := &explorerIterator{items: []any{make(chan int)}}
			view.iterator, view.transform = it, transform
			message := view.loadMoreData()().(tableItemMsg)
			require.ErrorAs(t, message.err, &unsupported)
			require.False(t, message.result.Exists())
			require.Len(t, view.rowData, 1)
			require.Equal(t, 1, it.index)
		})
	}
}

func TestExplorerTransformAfterEmptyObjects(t *testing.T) {
	for _, value := range []string{`"visible"`, `null`, `false`, `0`, `["visible"]`, `{"text":"visible"}`} {
		t.Run(value, func(t *testing.T) {
			initial, err := marshalItemsToJSONArray([]any{json.RawMessage(`{"projection":{}}`)}, "projection")
			require.NoError(t, err)
			view, err := newTableView("", gjson.ParseBytes(initial), false)
			require.NoError(t, err)
			it := &explorerIterator{items: []any{json.RawMessage(`{"projection":` + value + `}`)}}
			view.iterator, view.transform = it, "projection"
			viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
			viewer.resize(100, 24)
			command := explorerKey(viewer, "j")
			require.NotNil(t, command)
			viewer.Update(command())
			require.Len(t, view.rowData, 2)
			// The loaded value must be visible before a toggle rebuilds the table.
			require.Len(t, view.table.Rows()[1], 1)
			require.Equal(t, formatValue(gjson.Parse(value), false), view.table.Rows()[1][0])
			for i := 0; i < 3; i++ {
				view.table.SetCursor(1)
				explorerKey(viewer, "p")
				if value == `"visible"` {
					require.Equal(t, "visible", viewer.message)
				} else {
					require.JSONEq(t, value, viewer.message)
				}
				if viewer.canNavigateInto(view.rowData[1]) {
					explorerKey(viewer, "l")
					require.JSONEq(t, value, viewer.current().GetData().Raw)
					explorerKey(viewer, "h")
				}
				explorerKey(viewer, "r")
				require.Same(t, view, viewer.current())
				require.Same(t, it, view.iterator)
			}
			require.Equal(t, 1, it.index)
		})
	}
}
