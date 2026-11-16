package batches

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func applyBrowserDocumentCommand(t *testing.T, m *batchBrowserScreen, command tea.Cmd) {
	t.Helper()
	if command == nil {
		return
	}
	ready, ok := command().(batchBrowserDocumentReady)
	require.True(t, ok)
	require.NoError(t, ready.err)
	m.Update(ready)
}

func TestBatchBrowserTypedStylesSurviveWrappingWithoutStylingModelText(t *testing.T) {
	identity := "request\t界👩‍💻é"
	body := identity + "  Completed\n\nOutput\nOutcome: failed\nRequest: model text\n"
	d := browserTestDocument(t, body)
	d.decorate(RecordTextStyle{0, int64(len(identity)), "identity"})
	start := int64(len(identity) + 2)
	d.decorate(RecordTextStyle{start, start + int64(len("Completed")), "positive"})
	start = int64(strings.Index(body, "Output"))
	d.decorate(RecordTextStyle{start, start + int64(len("Output")), "heading"})
	for _, width := range []int{1, 12, 31, 80} {
		page, err := d.page(t.Context(), width, 0, 200)
		require.NoError(t, err)
		var status strings.Builder
		for i, spans := range page.Styles {
			for _, span := range spans {
				require.LessOrEqual(t, span.End, int64(len(page.Lines[i])))
				if span.Role == "positive" {
					status.WriteString(page.Lines[i][span.Start:span.End])
				}
			}
		}
		require.Equal(t, "Completed", status.String())
	}
	t.Setenv("COLORFGBG", "0")
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	m := newBatchBrowserDocumentScreen(t.Context(), d, "Result details", "batch / Record 1")
	t.Cleanup(m.documentState.stop)
	applyBrowserDocumentCommand(t, m, m.Init())
	view := m.View().Content
	fg, _ := batchBrowserTestTextStyle(t, view, "Completed")
	require.Equal(t, "141;201;160", fg)
	fg, bold := batchBrowserTestTextStyle(t, view, "Outcome: failed")
	require.Empty(t, fg)
	require.False(t, bold)
	fg, bold = batchBrowserTestTextStyle(t, view, "Request: model text")
	require.Empty(t, fg)
	require.False(t, bold)
	t.Setenv("NO_COLOR", "1")
	plain := m.View().Content
	require.NotContains(t, plain, "\x1b[")
	require.Equal(t, ansi.Strip(view), plain)
}

func TestBatchBrowserViewerPagesAndAnchorsAfterResize(t *testing.T) {
	var body strings.Builder
	for i := range 120 {
		fmt.Fprintf(&body, "Request: request-%03d\nInput: preserve this complete text through resize.\n\n", i)
	}
	document := browserTestDocument(t, body.String())
	m := newBatchBrowserDocumentScreen(t.Context(), document, "Input requests", "batch_test")
	t.Cleanup(m.documentState.stop)
	applyBrowserDocumentCommand(t, m, m.Init())
	require.Contains(t, m.View().Content, "request-000")
	_, page := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	applyBrowserDocumentCommand(t, m, page)
	before := m.documentState.page.Offset
	require.Positive(t, before)
	_, resize := m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	for _, line := range strings.Split(m.View().Content, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 50, "pending resize stays inside its frame")
	}
	applyBrowserDocumentCommand(t, m, resize)
	require.LessOrEqual(t, m.documentState.page.Offset, before)
	require.Less(t, before-m.documentState.page.Offset, int64(50), "resize stays at the same text")
	_, end := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	applyBrowserDocumentCommand(t, m, end)
	require.False(t, m.documentState.page.More)
	require.Contains(t, strings.Join(m.documentState.page.Lines, "\n"), "request-119")
	_, home := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	applyBrowserDocumentCommand(t, m, home)
	require.Zero(t, m.documentState.page.First)
}

func TestBatchBrowserViewerFramesRemainWithinTerminal(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	for _, size := range [][2]int{{30, 7}, {40, 10}, {50, 12}, {53, 14}, {80, 24}, {110, 32}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			d := browserTestDocument(t, strings.Repeat("Input: 界👩‍💻 one complete value across many display rows.\n", 30))
			m := newBatchBrowserDocumentScreen(t.Context(), d, "Input requests", "batch_a_very_long_identifier_that_must_stay_inside_the_frame")
			t.Cleanup(m.documentState.stop)
			_, command := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			applyBrowserDocumentCommand(t, m, command)
			view := m.View()
			require.True(t, view.AltScreen)
			require.NotContains(t, view.Content, "\x1b[", "NO_COLOR has no styling")
			lines := strings.Split(view.Content, "\n")
			require.LessOrEqual(t, len(lines), size[1])
			for _, line := range lines {
				require.LessOrEqual(t, ansi.StringWidth(line), size[0], line)
			}
			require.Contains(t, view.Content, "p print")
			require.Contains(t, view.Content, "Esc back")
			require.Contains(t, view.Content, "Input: ")
			_, rows := m.documentSize()
			require.Equal(t, rows, len(m.documentState.page.Lines))
		})
	}
}

func TestBatchDetailsHeaderLeavesRoomForTheFullID(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	id := "batch_" + strings.Repeat("abcdefgh", 6)
	d := browserTestDocument(t, "Completed  3 of 3 succeeded\nBatch ID  "+id+"\n")
	m := newBatchBrowserDocumentScreen(t.Context(), d, "Batch details", "")
	t.Cleanup(m.documentState.stop)
	_, resize := m.Update(tea.WindowSizeMsg{Width: 32, Height: 14})
	applyBrowserDocumentCommand(t, m, resize)
	view := strings.Split(m.View().Content, "\n")
	require.Contains(t, view[0], "Batch details")
	require.Contains(t, view[1], "─")
	require.Contains(t, view[2], "3 of 3 succeeded")
	for _, line := range view {
		require.LessOrEqual(t, ansi.StringWidth(line), 32)
	}
	require.Contains(t, strings.Join(m.documentState.page.Lines, ""), id, "wrapped lines retain the complete identifier")
	var printed strings.Builder
	require.NoError(t, d.writeTo(t.Context(), &printed))
	require.Equal(t, 1, strings.Count(printed.String(), id))
}

func TestBatchBrowserViewerIgnoresOldPageAndCancelsExit(t *testing.T) {
	m := newBatchBrowserDocumentScreen(t.Context(), browserTestDocument(t, "Input: first\nInput: second"), "Input requests", "batch_test")
	old := m.Init()
	_, replacement := m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	stale := old().(batchBrowserDocumentReady)
	require.Error(t, stale.err)
	m.Update(stale)
	require.False(t, m.closed)
	applyBrowserDocumentCommand(t, m, replacement)
	_, pending := m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.closed)
	require.Equal(t, "back", m.chosen)
	require.Error(t, pending().(batchBrowserDocumentReady).err)
}
