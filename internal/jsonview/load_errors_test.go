package jsonview

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/help"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type failedExplorerIterator struct{ err error }

func (it failedExplorerIterator) Next() bool   { return false }
func (it failedExplorerIterator) Current() any { panic("Current called after failed Next") }
func (it failedExplorerIterator) Err() error   { return it.err }

func TestExplorerStopsOnLazyLoadError(t *testing.T) {
	t.Parallel()
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "nested"}[nested], func(t *testing.T) {
			view, err := newTableView("", gjson.Parse(`[{"id":"first"}]`), false)
			require.NoError(t, err)
			failure := errors.New("synthetic page failure")
			view.iterator = failedExplorerIterator{failure}
			viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
			viewer.resize(80, 24)
			load := explorerKey(viewer, "j")
			require.NotNil(t, load)
			if nested {
				explorerKey(viewer, "l")
			}
			_, quit := viewer.Update(load())
			require.NotNil(t, quit, "a failed page must not leave a truncated result looking successful")
			require.IsType(t, tea.QuitMsg{}, quit())
			require.ErrorIs(t, viewer.loadErr, failure)
			require.False(t, view.isLoading)
			require.Len(t, view.rowData, 1)
		})
	}
}

func TestExplorerExhaustionIsNotAnError(t *testing.T) {
	t.Parallel()
	view, err := newTableView("", gjson.Parse(`["first"]`), false)
	require.NoError(t, err)
	view.iterator = failedExplorerIterator{}
	viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
	viewer.resize(80, 24)
	load := explorerKey(viewer, "j")
	require.NotNil(t, load)
	_, quit := viewer.Update(load())
	require.Nil(t, quit)
	require.NoError(t, viewer.loadErr)
	require.False(t, view.isLoading)
	require.Equal(t, "first", viewer.getSelectedContent())
}

func TestRunExplorerReturnsLazyLoadError(t *testing.T) {
	view, err := newTableView("", gjson.Parse(`["first"]`), false)
	require.NoError(t, err)
	failure := errors.New("synthetic page failure")
	view.iterator = failedExplorerIterator{failure}
	viewer := &JSONViewer{stack: []JSONView{view}, help: help.New()}
	viewer.resize(80, 24)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = runExplorer(viewer, tea.WithContext(ctx), tea.WithInput(strings.NewReader("j")), tea.WithOutput(io.Discard), tea.WithoutRenderer())
	require.ErrorIs(t, err, failure)
}
