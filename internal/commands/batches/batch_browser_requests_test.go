package batches

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func requestScreenFixture(t *testing.T, count int) *batchBrowserScreen {
	t.Helper()
	var records []string
	for i := range count {
		records = append(records, fmt.Sprintf(`{"custom_id":"request-%03d","body":{"input":"Message %03d"}}`, i+1, i+1))
	}
	m := newBatchBrowserRequests(t.Context(), catalogFixture(t, "input", records...), "Input requests", "batch_demo")
	t.Cleanup(m.requestState.stop)
	applyRequestCommand(t, m, m.Init())
	return m
}

func applyRequestCommand(t *testing.T, m *batchBrowserScreen, command tea.Cmd) {
	t.Helper()
	if command == nil {
		return
	}
	message := command()
	_, command = m.Update(message)
	require.Nil(t, command, "catalog completion must not start another scan")
	require.NoError(t, m.requestState.err)
}

func TestBatchBrowserRequestsSearchFocusAndSelection(t *testing.T) {
	m := requestScreenFixture(t, 3)
	require.Contains(t, ansi.Strip(m.View().Content), "> 1  request-001")
	require.Nil(t, m.View().Cursor)
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.True(t, m.searchFocused)
	require.Equal(t, tea.CursorBar, m.View().Cursor.Shape)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.False(t, m.searchFocused)
	_, command := m.Update(tea.PasteMsg{Content: "Message 003"})
	require.True(t, m.searchFocused)
	require.True(t, m.requestState.loading)
	require.NotContains(t, ansi.Strip(m.View().Content), "request-001")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.closed, "Enter must not open a stale match")
	applyRequestCommand(t, m, command)
	require.Equal(t, int64(1), m.requestState.page.Total)
	require.Contains(t, ansi.Strip(m.View().Content), "1 match")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "record:3", m.chosen)
	require.True(t, m.closed)
	require.Nil(t, m.Init(), "returning from details must retain the ready list without a loading flash")
	require.Equal(t, "Message 003", m.input.Value())
	require.Equal(t, int64(3), m.requestState.page.Rows[0].ID)
}

func TestBatchBrowserRequestsPagingEndAndResizePreserveSelection(t *testing.T) {
	m := requestScreenFixture(t, 100)
	_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	applyRequestCommand(t, m, command)
	require.Greater(t, m.requestState.selected, int64(0))
	_, command = m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	applyRequestCommand(t, m, command)
	require.Equal(t, int64(99), m.requestState.selected)
	require.Contains(t, ansi.Strip(m.View().Content), "> 100  request-100")
	_, command = m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	applyRequestCommand(t, m, command)
	require.Equal(t, int64(99), m.requestState.selected)
	require.Contains(t, ansi.Strip(m.View().Content), "> 100  request-100")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "record:100", m.chosen)
	// The session supplies size before Init when reopening this same screen.
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	applyRequestCommand(t, m, m.Init())
	require.Equal(t, int64(99), m.requestState.selected)
	_, command = m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	applyRequestCommand(t, m, command)
	require.Zero(t, m.requestState.selected)
	require.Contains(t, ansi.Strip(m.View().Content), "> 1  request-001")
}

func TestBatchBrowserRequestsReplacedSearchAndClosedScreenIgnoreLateResults(t *testing.T) {
	m := requestScreenFixture(t, 5)
	_, old := m.Update(tea.PasteMsg{Content: "request"})
	oldSequence := m.requestState.sequence
	_, latest := m.Update(tea.PasteMsg{Content: "-005"})
	oldMessage := old().(batchBrowserRequestsReady)
	require.ErrorIs(t, oldMessage.err, context.Canceled)
	require.Equal(t, oldSequence, oldMessage.sequence)
	m.Update(oldMessage)
	require.True(t, m.requestState.loading)
	require.False(t, m.closed)
	latestMessage := latest().(batchBrowserRequestsReady)
	m.Update(latestMessage)
	require.Equal(t, int64(5), m.requestState.page.Rows[0].ID)
	_, pending := m.Update(tea.WindowSizeMsg{Width: 90, Height: 25})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	message := pending().(batchBrowserRequestsReady)
	require.ErrorIs(t, message.err, context.Canceled)
	m.Update(message)
	require.Equal(t, "back", m.chosen)
	require.NoError(t, m.requestState.err)
	require.True(t, m.closed)
}

func TestBatchBrowserRequestsFailureAndViewAllChoices(t *testing.T) {
	m := requestScreenFixture(t, 3)
	failure := errors.New("synthetic catalog failure")
	m.Update(batchBrowserRequestsReady{screen: m, sequence: m.requestState.sequence, err: failure})
	require.ErrorIs(t, m.requestState.err, failure)
	require.True(t, m.closed)
	m = requestScreenFixture(t, 3)
	m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	require.Equal(t, "all", m.chosen)
	require.True(t, m.closed)
	m = requestScreenFixture(t, 3)
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	require.Empty(t, m.chosen)
	require.True(t, m.closed)
}

func TestBatchBrowserRequestsGeometryUnicodeAndNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	catalog := catalogFixture(t, "input",
		`{"custom_id":"same","body":{"input":"I love this."}}`,
		`{"custom_id":"same","body":{"input":"This is awful."}}`,
		`{"custom_id":"界🙂é\u001b[31m","body":{"input":"The package arrived.\nAnother line."}}`)
	for _, size := range [][2]int{{1, 1}, {29, 7}, {30, 7}, {40, 12}, {50, 12}, {68, 12}, {80, 24}, {120, 42}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			m := newBatchBrowserRequests(t.Context(), catalog, "Input requests", "batch_demo")
			t.Cleanup(m.requestState.stop)
			_, command := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			applyRequestCommand(t, m, command)
			for _, search := range []string{"", "界🙂é"} {
				m.input.SetValue(search)
				m.input.CursorEnd()
				m.searchFocused = search != ""
				view := m.View()
				require.NotContains(t, view.Content, "\x1b", "NO_COLOR must remove all SGR styles")
				lines := strings.Split(view.Content, "\n")
				require.LessOrEqual(t, len(lines), size[1])
				for _, line := range lines {
					require.LessOrEqual(t, ansi.StringWidth(line), size[0], "%q", line)
				}
				if size[0] >= 30 && size[1] >= 7 {
					for _, text := range []string{"Input requests", "batch_demo", "Search", "Enter", "Ctrl+V", "Esc"} {
						require.Contains(t, view.Content, text)
					}
					if view.Cursor != nil {
						require.Less(t, view.Cursor.Position.X, size[0])
						require.Less(t, view.Cursor.Position.Y, size[1])
					}
				}
			}
		})
	}
}

func TestBatchBrowserRequestsEmptySearchKeepsEditingAndBlocksOpen(t *testing.T) {
	m := requestScreenFixture(t, 3)
	_, command := m.Update(tea.PasteMsg{Content: "no-match"})
	applyRequestCommand(t, m, command)
	require.Contains(t, ansi.Strip(m.View().Content), "No matching requests.")
	require.True(t, m.searchFocused)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.closed)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.True(t, m.searchFocused)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, "back", m.chosen)
}
