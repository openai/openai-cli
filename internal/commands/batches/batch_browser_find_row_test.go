package batches

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestBatchBrowserFindRowList(t *testing.T) {
	c := &batchBrowserCSVRows{document: browserTestDocument(t, "")}
	for i, fields := range [][]string{{"007", "Invoice?", "Open Billing."}, {"003", "Password?", "Reset it."}, {"012", "Billing email?", "Change your email."}} {
		require.NoError(t, c.add(i+1, []string{"Ticket", "Question"}, fields[:2], fields[2], ""))
	}
	m := newBatchBrowserCSVList(t.Context(), c, "tickets.csv")
	defer m.requestState.stop()
	applyRequestCommand(t, m, m.Init())
	update := func(msg tea.Msg) {
		_, cmd := m.Update(msg)
		for cmd != nil {
			_, cmd = m.Update(cmd())
		}
		require.NoError(t, m.requestState.err)
	}
	key := func(code rune) { update(tea.KeyPressMsg{Code: code}) }
	text := func(s string) { update(tea.PasteMsg{Content: s}) }
	view := func() string { return ansi.Strip(m.View().Content) }
	require.Contains(t, view(), "3 rows")
	for _, id := range []string{"007", "003", "012"} {
		require.Contains(t, view(), id)
	}
	update(tea.KeyPressMsg{Code: '/', Text: "/"})
	text("billing")
	require.Contains(t, view(), "2 matches")
	require.Contains(t, view(), "Open Billing")
	require.Contains(t, view(), "Billing email")
	require.NotContains(t, view(), "003")
	key(tea.KeyEnter)
	require.Contains(t, view(), "Question: Invoice?")
	key(tea.KeyEscape)
	require.Equal(t, "billing", m.input.Value())
	key(tea.KeyDown)
	key(tea.KeyEnter)
	require.Contains(t, view(), "Answer: Change your email.")
	key(tea.KeyEscape)
	key(tea.KeyUp)
	key(tea.KeyUp)
	text("password")
	require.Contains(t, view(), "003")
	require.NotContains(t, view(), "012")
	key(tea.KeyEscape)
	require.Contains(t, view(), "3 rows")
	update(tea.KeyPressMsg{Code: '/', Text: "/"})
	text("nosuchrow")
	require.Contains(t, view(), "No matching rows.")
	key(tea.KeyLeft)
	key(tea.KeyRight)
	key(tea.KeyEscape)
	require.Contains(t, view(), "3 rows")
}

func TestBatchBrowserFindRowEnterDuringSearch(t *testing.T) {
	c := &batchBrowserCSVRows{document: browserTestDocument(t, "")}
	for i, value := range []string{"before", "after"} {
		require.NoError(t, c.add(i+1, []string{"ID"}, []string{value}, "answer", ""))
	}
	m := newBatchBrowserCSVList(t.Context(), c, "rows.csv")
	defer m.requestState.stop()
	applyRequestCommand(t, m, m.Init())
	_, old := m.Update(tea.PasteMsg{Content: "bef"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	_, latest := m.Update(tea.PasteMsg{Content: "after"})
	m.Update(old())
	require.False(t, m.requestState.expanded)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, preview := m.Update(latest())
	require.NotNil(t, preview)
	m.Update(preview())
	require.True(t, m.requestState.expanded)
	require.Contains(t, ansi.Strip(m.View().Content), "ID: after")
}

func TestBatchBrowserFindRowWholeFieldAndPagination(t *testing.T) {
	c := &batchBrowserCSVRows{document: browserTestDocument(t, "")}
	long := strings.Repeat("界", 30000) + "Ünicode/BILL"
	for i := 1; i <= 70; i++ {
		require.NoError(t, c.add(i, []string{"Ticket"}, []string{fmt.Sprint(i)}, long, ""))
	}
	p, err := c.page(t.Context(), "ünicode/bill", 68, 3)
	require.NoError(t, err)
	require.EqualValues(t, 70, p.Total)
	require.EqualValues(t, 67, p.First)
	require.EqualValues(t, 70, p.Rows[2].ID)
	require.Contains(t, p.Rows[2].Preview, "Ünicode/BILL")
	require.NotContains(t, p.Rows[2].Preview, "�")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = c.page(ctx, "missing", 0, 4)
	require.ErrorIs(t, err, context.Canceled)
	p, err = c.page(t.Context(), "absent", 0, 2)
	require.NoError(t, err)
	require.Zero(t, p.Total)
	p, err = c.page(t.Context(), "", 68, 3)
	require.NoError(t, err)
	require.EqualValues(t, 70, p.Total)
	for _, size := range [][2]int{{30, 7}, {43, 10}, {80, 20}} {
		m := newBatchBrowserCSVList(t.Context(), c, "tickets.csv")
		_, cmd := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		applyRequestCommand(t, m, cmd)
		lines := strings.Split(m.View().Content, "\n")
		require.LessOrEqual(t, len(lines), size[1])
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
		m.requestState.stop()
	}
}

// Measure an uncached query against prepared rows, including a visible results page.
func BenchmarkBatchCSVFirstSearch(b *testing.B) {
	for _, count := range []int{10000, 50000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			doc, err := newBatchBrowserDocument(b.Context())
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { doc.close() })
			c := &batchBrowserCSVRows{document: doc}
			for i := 1; i <= count; i++ {
				answer := "Check the status of the customer's ticket."
				if i == 1 {
					answer = "first-marker"
				} else if i == count {
					answer = "last-marker"
				}
				if err := c.add(i, []string{"Ticket"}, []string{fmt.Sprintf("T-%05d", i)}, answer, ""); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for b.Loop() {
				c.query = ""
				page, err := c.page(b.Context(), "last-marker", 0, 16)
				if err != nil || page.Total != 1 || page.Rows[0].ID != int64(count) {
					b.Fatalf("unexpected result: %v %v", page, err)
				}
			}
		})
	}
}

func TestBatchBrowserCSVInlineHeaderAndFooter(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	c := &batchBrowserCSVRows{document: browserTestDocument(t, "")}
	require.NoError(t, c.add(1, []string{"Ticket"}, []string{"007"}, "Billing", ""))
	m := newBatchBrowserCSVList(t.Context(), c, "tickets.csv")
	defer m.requestState.stop()
	m.notice = "Saved tickets-results.csv"
	applyRequestCommand(t, m, m.Init())
	for _, size := range [][2]int{{114, 37}, {55, 12}, {30, 7}} {
		_, cmd := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		applyRequestCommand(t, m, cmd)
		v := m.View()
		require.False(t, v.AltScreen)
		require.Contains(t, v.Content, "tickets.csv · 1 row")
		require.NotContains(t, v.Content, "Rows with answers")
		require.NotContains(t, v.Content, "ID or text")
		lines := strings.Split(v.Content, "\n")
		require.Empty(t, lines[len(lines)-1], "leave a line for shell cleanup")
		footerAt := strings.Index(v.Content, "↑↓ select")
		require.Greater(t, footerAt, strings.Index(v.Content, "Saved tickets-results.csv"))
		if m.hasFrame() {
			require.Equal(t, "╭"+strings.Repeat("─", size[0]-2)+"╮", lines[0])
			bottom := "╰" + strings.Repeat("─", size[0]-2) + "╯"
			require.Contains(t, v.Content, bottom+"\n  ↑↓ select")
			require.True(t, strings.HasSuffix(lines[len(lines)-2], "Ctrl+G expand"))
		} else {
			require.NotContains(t, v.Content, "╭")
		}
		require.LessOrEqual(t, len(lines), min(size[1], 12))
		_, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		applyRequestCommand(t, m, cmd)
		full := m.View()
		require.True(t, full.AltScreen)
		fullLines := strings.Split(full.Content, "\n")
		require.Len(t, fullLines, size[1])
		require.True(t, strings.HasSuffix(fullLines[size[1]-1], "Ctrl+G compact"))
		require.Contains(t, full.Content, "007")
		for _, line := range fullLines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
		_, cmd = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		applyRequestCommand(t, m, cmd)
		require.Equal(t, v.Content, m.View().Content)
		for _, line := range lines {
			require.LessOrEqual(t, ansi.StringWidth(line), size[0])
		}
	}
	m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	v := m.View()
	require.Equal(t, 1, v.Cursor.Position.Y)
	require.Contains(t, v.Content, "ID or text")
	_, cmd := m.Update(tea.PasteMsg{Content: "missing"})
	applyRequestCommand(t, m, cmd)
	v = m.View()
	require.Contains(t, v.Content, "Esc clear")
	require.NotContains(t, v.Content, "Enter expand")
	// The parent browser still returns to its original screen after the inline view.
	require.True(t, newBatchBrowserList(batchBrowsePage{}, false).View().AltScreen)
}

func TestCSVRowListUsesSizeChosenInRowDetail(t *testing.T) {
	rows := &batchBrowserCSVRows{document: browserTestDocument(t, "")}
	for i := 1; i <= 60; i++ {
		require.NoError(t, rows.add(i, []string{"Ticket"}, []string{fmt.Sprint(i)}, "billing", ""))
	}
	list := newBatchBrowserCSVList(t.Context(), rows, "tickets.csv")
	defer list.requestState.stop()
	list.width, list.height = 90, 25
	list.input.SetValue("billing")
	applyRequestCommand(t, list, list.Init())
	list.requestState.selected = 3
	compactCount := len(list.requestState.page.Rows)
	action, err := rows.showRow(t.Context(), 4, "tickets.csv", func(_ context.Context, detail *batchBrowserScreen) error {
		detail.width, detail.height = list.width, list.height
		_, cmd := detail.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		require.NotNil(t, cmd)
		detail.Update(cmd())
		require.NoError(t, detail.documentState.err)
		require.True(t, detail.View().AltScreen)
		return nil
	})
	require.NoError(t, err)
	require.Empty(t, action)
	applyRequestCommand(t, list, list.Init())
	require.True(t, list.View().AltScreen)
	require.Len(t, list.requestState.page.Rows, list.requestRows())
	require.Greater(t, list.requestRows(), compactCount)
	require.Equal(t, int64(3), list.requestState.selected)
	require.Equal(t, "billing", list.input.Value())
}
