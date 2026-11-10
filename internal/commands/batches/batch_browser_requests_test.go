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
	require.False(t, m.closed)
	require.True(t, m.requestState.expanded)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
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
	require.Contains(t, ansi.Strip(m.View().Content), "Message 100")
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
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

func TestBatchBrowserRequestsInlinePreviewFollowsSelection(t *testing.T) {
	m := requestScreenFixture(t, 3)
	m.requestState.preview.read = func(ctx context.Context, id int64) ([]batchBrowserPreviewLine, error) {
		return []batchBrowserPreviewLine{{fmt.Sprintf("Usage for %d", id), "secondary"}}, ctx.Err()
	}
	_, first := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, second := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	stale := first().(batchBrowserPreviewReady)
	require.ErrorIs(t, stale.err, context.Canceled)
	m.Update(stale)
	applyRequestCommand(t, m, second)
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "> 2  request-002")
	require.Contains(t, view, "Usage for 2")
	require.NotContains(t, view, "Usage for 1")
	_, third := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	applyRequestCommand(t, m, third)
	require.False(t, m.closed)
	require.False(t, m.requestState.expanded)
	require.NotContains(t, ansi.Strip(m.View().Content), "Usage for")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.closed)
}

func TestBatchBrowserRequestsPreviewKeepsAnswerAndTrustedOutcome(t *testing.T) {
	m := requestScreenFixture(t, 3)
	m.requestState.catalog.selection = "output"
	row := &m.requestState.page.Rows[0]
	row.Preview = "neutral"
	m.requestState.expanded = true
	lines := []batchBrowserPreviewLine{{"Completed", "positive"}, {"Output", "heading"}, {"neutral", ""}, {"Tokens  31 input  5 output  36 total", "secondary"}}
	m.requestState.preview.lines = lines
	view := ansi.Strip(m.View().Content)
	require.Equal(t, 1, strings.Count(view, "neutral"))
	require.Equal(t, 1, strings.Count(view, "request-001"))
	require.Contains(t, view, "Completed")
	require.Contains(t, view, "Tokens  31 input")
	require.NotContains(t, view, "Output")
	m.width, m.height = 30, 7
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "neutral")
	require.LessOrEqual(t, len(strings.Split(view, "\n")), m.height)
	m.width, m.height = 80, 24
	m.requestState.preview.lines = []batchBrowserPreviewLine{{"Output", ""}, {"neutral", ""}}
	require.Contains(t, ansi.Strip(m.View().Content), "Output", "untrusted model text must never identify a section")
}

func TestBatchBrowserInlineInputsAndFailuresAddContextWithoutRepeatingTheRow(t *testing.T) {
	m := requestScreenFixture(t, 3)
	row := &m.requestState.page.Rows[0]
	m.requestState.expanded = true
	row.Preview = "This is awful."
	m.requestState.preview.lines = []batchBrowserPreviewLine{{"Input", "heading"}, {row.Preview, ""},
		{"Instructions", "heading"}, {"Classify the message.", ""}, {"Settings", "heading"},
		{"Model", "field"}, {"synthetic-model", ""}}
	view := ansi.Strip(m.View().Content)
	require.Equal(t, 1, strings.Count(view, row.Preview))
	require.Regexp(t, `Instructions +Classify the message`, view)
	require.Regexp(t, `Model +synthetic-model`, view)
	require.NotContains(t, view, "Settings")
	m.requestState.catalog.selection = "error"
	row.Preview = "Temporary issue."
	m.requestState.preview.lines = []batchBrowserPreviewLine{{"Failed", "negative"}, {row.Preview, "record_message"},
		{"Next", "field"}, {"Retry this request.", ""}, {"HTTP", "field"}, {"500", ""}}
	view = ansi.Strip(m.View().Content)
	require.Equal(t, 1, strings.Count(view, row.Preview))
	require.Regexp(t, `Next +Retry this request`, view)
	require.Regexp(t, `HTTP +500`, view)
	for _, size := range [][2]int{{30, 7}, {45, 10}, {50, 12}} {
		m.width, m.height = size[0], size[1]
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		require.LessOrEqual(t, len(lines), size[1])
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
	}
}

func TestBatchBrowserSingleErrorStartsExpandedAndEscapeReturns(t *testing.T) {
	row := `{"custom_id":"r","error":{"code":"bad_input","message":"Check input."}}`
	newErrors := func(items ...string) *batchBrowserScreen {
		m := newBatchBrowserRequests(t.Context(), catalogFixture(t, "error", items...), "Request errors", "batch_demo")
		t.Cleanup(m.requestState.stop)
		applyRequestCommand(t, m, m.Init())
		return m
	}
	m := newErrors(row)
	require.True(t, m.requestState.expanded)
	require.Contains(t, ansi.Strip(m.View().Content), "Esc back")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.closed)
	require.Equal(t, "back", m.chosen)

	m = newErrors(row)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, m.requestState.expanded)
	_, resize := m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	applyRequestCommand(t, m, resize)
	require.False(t, m.requestState.expanded, "resizing must preserve an explicit collapse")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, m.requestState.expanded)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, m.closed, "Escape collapses an explicitly expanded row")
	require.False(t, m.requestState.expanded)
	require.False(t, newErrors(row, row).requestState.expanded)
}
