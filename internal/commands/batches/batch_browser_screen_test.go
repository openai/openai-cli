package batches

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/terminal"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestBatchBrowserLayoutCountsAndDetails(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	page := batchBrowsePage{items: []gjson.Result{
		gjson.Parse(`{"id":"batch_complete","status":"completed","endpoint":"/v1/responses","request_counts":{"completed":50000,"total":50000,"failed":0}}`),
		gjson.Parse(`{"id":"batch_partial","status":"completed","endpoint":"/v1/responses","request_counts":{"completed":2,"total":3,"failed":1}}`),
		gjson.Parse(`{"id":"batch_unknown","status":"validating","endpoint":"/v1/responses"}`),
	}}
	m := newBatchBrowserList(page, false)
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "3 batches")
	require.NotContains(t, view, "loaded")
	require.True(t, strings.HasPrefix(view, "╭"))
	require.Contains(t, view, "│  Search  ")
	require.Contains(t, view, "Filter this page")
	require.Contains(t, view, "Succeeded  Failed")
	require.Contains(t, view, "50000/50000")
	require.Contains(t, view, "2/3")
	require.Contains(t, view, "—")
	require.Contains(t, view, "  Endpoint: /v1/responses")
	require.Contains(t, view, "Esc exit")
	require.NotContains(t, view, "Search covers")
	require.NotContains(t, view, "Quit")
	require.NotContains(t, view, "  Refresh")
	require.Contains(t, view, "  ↑↓ move  Enter open  Ctrl+N create  Ctrl+R refresh  Esc exit")
	countEnd, failedEnd := 0, 0
	for _, line := range strings.Split(view, "\n") {
		line = strings.TrimRight(strings.TrimSuffix(line, "│"), " ")
		if strings.HasSuffix(line, "3 batches") {
			countEnd = ansi.StringWidth(line)
		}
		if strings.HasSuffix(line, "Failed") {
			failedEnd = ansi.StringWidth(line)
		}
	}
	require.Positive(t, countEnd)
	require.Equal(t, countEnd, failedEnd, "batch count must end at the Failed column")
	m.index = 1
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "Failed 1  Succeeded 2/3")
	require.Contains(t, view, "completed")
}

func TestBatchBrowserLayoutEmptySearch(t *testing.T) {
	m := newBatchBrowserList(batchBrowsePage{}, false)
	require.Contains(t, ansi.Strip(m.View().Content), "No batches on this page.")
	m.setPage(batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one"}`)}, next: "batch_one"}, true)
	m.input.SetValue("no_match")
	m.filter()
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "No matches on this page.")
	for _, control := range []string{"Previous page", "Next page", "Ctrl+R refresh"} {
		require.Contains(t, view, control)
	}
}

func TestBatchBrowserLayoutActionDetails(t *testing.T) {
	m := newBatchBrowserActions("batch_short")
	m.overview = gjson.Parse(`{"status":"completed","request_counts":{"completed":2,"failed":1,"total":3}}`)
	view := ansi.Strip(m.View().Content)
	for _, text := range []string{"batch_short", "> View results", "Completed", "2 of 3 succeeded", "1 failed", "Batch details", "Ctrl+R refresh", "Esc back"} {
		require.Contains(t, view, text)
	}
	for _, text := range []string{"Read the returned answers", "Refresh counts, progress", "Pattern (", "  Back", "Status"} {
		require.NotContains(t, view, text)
	}
	m.width = 160
	require.NotContains(t, ansi.Strip(m.View().Content), "Pattern (")
	require.Equal(t, []string{"View results", "View errors", "View input", "Batch details", "Save a file"}, []string{m.choices[0].title, m.choices[1].title, m.choices[2].title, m.choices[3].title, m.choices[4].title})
}

func TestBatchBrowserActionsNarrowOverviewAndRefresh(t *testing.T) {
	for _, batch := range []string{
		`{"status":"in_progress","request_counts":{"completed":50000,"failed":0,"total":50000}}`,
		`{"status":"validating","request_counts":{"completed":null,"failed":"0","total":-1}}`,
	} {
		m := newBatchBrowserActions("batch_one")
		m.overview = gjson.Parse(batch)
		m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
		for i := range m.choices {
			m.index = i
			view := ansi.Strip(m.View().Content)
			require.Len(t, strings.Split(view, "\n"), 7, view)
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), 30, line)
			}
			for _, text := range []string{"batch_one", "> " + m.choices[i].title, "Ctrl+R", "Esc"} {
				require.Contains(t, view, text)
			}
			if m.overview.Get("status").Str == "validating" {
				require.Equal(t, 3, strings.Count(view, "—"))
				require.NotContains(t, view, " 0")
			} else {
				require.Equal(t, 2, strings.Count(view, "50000"))
				require.Contains(t, view, "In progress")
			}
		}
		m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
		require.Equal(t, "refresh", m.chosen)
		require.Equal(t, 4, m.index)
		require.Empty(t, m.View().Content)
	}
}

func TestBatchBrowserActionsNarrowMaximumCountsAndLongStatus(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	m.overview = gjson.Parse(fmt.Sprintf(`{"status":%q,"request_counts":{"completed":18446744073709551615,"failed":18446744073709551615,"total":18446744073709551615}}`, strings.Repeat("future_界", 100)+"\u001b[31m"))
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	view := ansi.Strip(m.View().Content)
	require.Len(t, strings.Split(view, "\n"), 7, view)
	for _, line := range strings.Split(view, "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 30, line)
	}
	for _, text := range []string{"batch_one", "Succeeded:18446744073709551615", "Failed:18446744073709551615", "Total:18446744073709551615", "> View results", "Ctrl+R", "Esc"} {
		require.Contains(t, view, text)
	}
	require.NotContains(t, m.View().Content, "\x1b[31m")
}

func TestBatchBrowserLayoutBounds(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{
		gjson.Parse(`{"id":"batch_界🙂é_\u001b[31m","status":"in_progress","endpoint":"/v1/responses","request_counts":{"completed":50000,"total":50000,"failed":0}}`),
		gjson.Parse(fmt.Sprintf(`{"id":%q,"status":"completed","endpoint":"/v1/responses","request_counts":{"completed":2,"total":3,"failed":1}}`, "batch_"+strings.Repeat("long", 20))),
	}, next: "batch_next"}
	for _, size := range [][2]int{{1, 1}, {29, 7}, {30, 7}, {40, 12}, {68, 12}, {80, 24}, {120, 42}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			screens := []*batchBrowserScreen{
				newBatchBrowserList(page, true),
				newBatchBrowserActions("batch_" + strings.Repeat("long", 20)),
				newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output"),
				newBatchBrowserReturn("Output is above. Use terminal scrollback to read earlier records."),
				newBatchBrowserList(batchBrowsePage{}, false),
			}
			for _, screen := range screens {
				screen.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for index := 0; index < max(1, len(screen.visible)); index++ {
					screen.index = index
					view := ansi.Strip(screen.View().Content)
					lines := strings.Split(view, "\n")
					require.LessOrEqual(t, len(lines), size[1], "%s: %q", screen.kind, view)
					for _, line := range lines {
						require.LessOrEqual(t, ansi.StringWidth(line), size[0], "%s: %q", screen.kind, line)
					}
					require.NotContains(t, view, "\x1b[31m")
				}
			}
		})
	}
}

func TestBatchBrowserClearsCompletedNavigationFrames(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one"}`)}}
	for _, screen := range []*batchBrowserScreen{
		newBatchBrowserList(page, false),
		newBatchBrowserActions("batch_one"),
		newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output"),
		newBatchBrowserReturn("Results are above. Use terminal scrollback."),
	} {
		t.Run(screen.kind, func(t *testing.T) {
			if screen.kind == "path" {
				screen.input.SetValue("")
				screen.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.NotEmpty(t, screen.View().Content, "invalid input must retain the form")
				screen.input.SetValue("new-output.jsonl")
			}
			require.NotEmpty(t, screen.View().Content)
			screen.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			require.Empty(t, screen.View().Content, "completed menus must not remain above the next screen")
			screen.Init()
			require.NotEmpty(t, screen.View().Content, "reused screens must render again")
			screen.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			require.Empty(t, screen.View().Content)
		})
	}
}

func TestBatchBrowserSearchAcceptsDisplayedAndRawStatus(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{
		gjson.Parse(`{"id":"batch_complete","status":"completed"}`),
		gjson.Parse(`{"id":"batch_working","status":"in_progress"}`),
	}}
	m := newBatchBrowserList(page, false)
	for _, query := range []string{"in progress", "in_progress", "IN PROGRESS"} {
		m.input.SetValue(query)
		m.filter()
		require.Equal(t, []int{1}, m.visible, query)
		require.Equal(t, "batch:batch_working", m.choices[m.visible[0]].value, query)
	}
}

func TestBatchBrowserSearchCaretAndRefresh(t *testing.T) {
	m := newBatchBrowserList(batchBrowsePage{}, false)
	for _, test := range []struct {
		value, visible     string
		position, width, x int
	}{
		{"界🙂é", "界🙂é", 4, 20, 5},
		{"界🙂é", "界🙂é", 3, 20, 4},
		{"abcdefghijkl", "hijkl", 12, 6, 5},
		{"abcdefghijkl", "bcdefg", 6, 6, 5},
		{"界🙂x", "🙂x", 3, 4, 3},
	} {
		m.input.SetValue(test.value)
		m.input.SetCursor(test.position)
		text, x := m.searchView(test.width)
		require.Equal(t, test.visible, text)
		require.Equal(t, test.x, x)
	}
	m.input.SetValue("")
	m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.Equal(t, "r", m.input.Value())
	require.Empty(t, m.chosen, "ordinary r must remain search text")
	for _, size := range [][2]int{{30, 7}, {80, 24}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()
		require.NotNil(t, view.Cursor)
		require.Equal(t, tea.CursorBar, view.Cursor.Shape)
		offset := 0
		if size[0] >= 50 && size[1] >= 12 {
			offset = 1
		}
		require.Equal(t, offset, view.Cursor.Position.Y)
		require.Equal(t, 11+offset, view.Cursor.Position.X)
		require.Contains(t, ansi.Strip(view.Content), "Ctrl+R refresh")
	}
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.Equal(t, "refresh", m.chosen)
	require.Equal(t, "r", m.input.Value())
	require.Empty(t, m.View().Content)
	require.Nil(t, m.View().Cursor)
}

func TestBatchBrowserSearchFocusNavigation(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{
		gjson.Parse(`{"id":"batch_one","endpoint":"/v1/responses"}`),
		gjson.Parse(`{"id":"batch_two","endpoint":"/v1/responses"}`),
	}, next: "batch_two"}
	m := newBatchBrowserList(page, false)
	require.Nil(t, m.View().Cursor)
	require.Contains(t, ansi.Strip(m.View().Content), "> batch_one")
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.NotNil(t, m.View().Cursor)
	require.NotContains(t, ansi.Strip(m.View().Content), "> batch_one")
	require.Contains(t, ansi.Strip(m.View().Content), "↓ batches")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Nil(t, m.View().Cursor)
	require.Equal(t, 0, m.index)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.NotNil(t, m.View().Cursor)
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Nil(t, m.View().Cursor)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, m.index)
	m.Update(tea.PasteMsg{Content: "two"})
	require.NotNil(t, m.View().Cursor)
	require.Equal(t, "two", m.input.Value())
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Contains(t, ansi.Strip(m.View().Content), "> batch_two")
	m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	require.Contains(t, ansi.Strip(m.View().Content), "No matches on this page.")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Empty(t, m.chosen, "Search must not open a page control when no batches match")
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Contains(t, ansi.Strip(m.View().Content), "> Next page")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "next", m.chosen)

	empty := newBatchBrowserList(batchBrowsePage{}, false)
	empty.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.NotNil(t, empty.View().Cursor, "empty lists must retain a reachable Search field")
}

func TestBatchBrowserSeparatesDetailsAndControls(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one","endpoint":"/v1/responses"}`)}}
	for _, size := range [][2]int{{30, 7}, {40, 9}, {80, 10}, {80, 11}, {80, 24}} {
		m := newBatchBrowserList(page, false)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(ansi.Strip(m.View().Content), "\n")
		found := false
		for i, line := range lines {
			if strings.Contains(line, "↑↓ move") {
				require.Greater(t, i, 0)
				require.Empty(t, strings.Trim(strings.TrimSpace(lines[i-1]), "│ "), "size %v: details need a blank before controls", size)
				found = true
			}
		}
		require.True(t, found, "size %v: controls must remain visible", size)
		require.LessOrEqual(t, len(lines), size[1])
	}
}

func TestBatchBrowserUnmatchedSearchKeepsControlsInBounds(t *testing.T) {
	m := newBatchBrowserList(batchBrowsePage{
		items: []gjson.Result{gjson.Parse(`{"id":"batch_one"}`)}, next: "batch_one",
	}, false)
	m.input.SetValue("no_match")
	m.filter()
	m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
	for _, focused := range []bool{false, true} {
		m.searchFocused = focused
		view := ansi.Strip(m.View().Content)
		require.LessOrEqual(t, len(strings.Split(view, "\n")), 7, "search focus %v: %q", focused, view)
		for _, text := range []string{"Search  ", "No matches", "Next page", "Ctrl+R refresh", "Esc exit"} {
			require.Contains(t, view, text)
		}
	}
}

func TestBatchBrowserMenuOwnsAlternateScreen(t *testing.T) {
	page := batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one","status":"completed"}`)}}
	for _, screen := range []*batchBrowserScreen{
		newBatchBrowserList(page, false),
		newBatchBrowserActions("batch_one"),
		newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output"),
		newBatchBrowserReturn("Printed results remain above."),
	} {
		menu := screen.kind != "return"
		require.Equal(t, menu, screen.View().AltScreen, screen.kind)
		screen.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
		view := screen.View()
		require.Empty(t, view.Content, screen.kind)
		require.Nil(t, view.Cursor, screen.kind)
		require.Equal(t, menu, view.AltScreen, "screen ownership must survive the final frame: %s", screen.kind)
	}
}

func TestBatchBrowserPanelUsesTerminalAndAnchorsFooter(t *testing.T) {
	for _, size := range [][2]int{{30, 7}, {40, 12}, {80, 24}, {160, 48}} {
		m := newBatchBrowserList(batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one","status":"completed","request_counts":{"completed":50000,"total":50000,"failed":0}}`)}}, false)
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := m.View()
		lines := strings.Split(ansi.Strip(view.Content), "\n")
		require.Len(t, lines, size[1])
		footerRow := size[1] - 1
		if size[0] >= 50 && size[1] >= 12 {
			require.Equal(t, size[0], ansi.StringWidth(lines[0]))
			require.True(t, strings.HasPrefix(lines[0], "╭"))
			require.True(t, strings.HasSuffix(lines[len(lines)-1], "╯"))
			footerRow--
		} else {
			require.NotContains(t, view.Content, "╭")
		}
		require.Contains(t, lines[footerRow], "Esc exit")
	}
}

func TestBatchBrowserPanelSelectionColors(t *testing.T) {
	for _, theme := range []struct{ background, fill, text string }{
		{"0", "48;2;52;61;88", "38;2;242;244;250"},
		{"15", "48;2;233;239;254", "38;2;32;40;59"},
	} {
		t.Run(theme.background, func(t *testing.T) {
			t.Setenv("COLORFGBG", theme.background)
			t.Setenv("FORCE_COLOR", "")
			t.Setenv("NO_COLOR", "")
			m := newBatchBrowserList(batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one"}`)}}, false)
			view := m.View().Content
			require.Contains(t, view, theme.fill)
			require.Contains(t, view, theme.text)
			require.Contains(t, ansi.Strip(view), "> batch_one")
			m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
			require.NotContains(t, m.View().Content, theme.fill, "Search focus must remove the row highlight")
			m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			t.Setenv("NO_COLOR", "1")
			view = m.View().Content
			require.NotContains(t, view, "48;2;")
			require.Contains(t, ansi.Strip(view), "> batch_one")
		})
	}
}

func TestBatchBrowserSelectedBackgroundCoversEveryRowCell(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("COLORFGBG", "0")
	for _, m := range []*batchBrowserScreen{
		newBatchBrowserList(batchBrowsePage{items: []gjson.Result{gjson.Parse(`{"id":"batch_one","status":"completed","request_counts":{"completed":2,"failed":1,"total":3}}`)}}, false),
		newBatchBrowserActions("batch_one"),
	} {
		t.Run(m.kind, func(t *testing.T) {
			var selectedRow string
			for _, line := range strings.Split(m.View().Content, "\n") {
				if strings.Contains(ansi.Strip(line), "> ") {
					selectedRow = line
					break
				}
			}
			require.NotEmpty(t, selectedRow)
			background, column := false, 0
			for selectedRow != "" {
				sequence, cells, read := terminal.Sequence(selectedRow)
				selectedRow = selectedRow[read:]
				if strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") {
					parameters := strings.Split(sequence[2:len(sequence)-1], ";")
					for i := 0; i < len(parameters); i++ {
						switch parameters[i] {
						case "", "0", "49":
							background = false
						case "38", "48":
							if parameters[i] == "48" {
								background = true
							}
							if i+1 < len(parameters) && parameters[i+1] == "2" {
								i += 4
							} else if i+1 < len(parameters) && parameters[i+1] == "5" {
								i += 2
							}
						}
					}
				}
				if cells > 0 && column > 0 && column < m.width-1 {
					require.True(t, background, "selected cell %d lost its background", column)
				}
				column += cells
			}
			require.Equal(t, m.width, column)
		})
	}
}

func TestBatchBrowserStatusSemanticTones(t *testing.T) {
	for _, tc := range []struct{ status, counts, tone string }{
		{"validating", `{}`, "active"},
		{"in_progress", `{"completed":3,"failed":0,"total":3}`, "active"},
		{"finalizing", `{}`, "active"},
		{"failed", `{}`, "negative"},
		{"expired", `{}`, "negative"},
		{"cancelling", `{}`, "warning"},
		{"cancelled", `{"completed":3,"failed":0,"total":3}`, "warning"},
		{"future_status", `{}`, ""},
		{"completed", `{"completed":3,"failed":0,"total":3}`, "positive"},
		{"completed", `{"completed":2,"failed":1,"total":3}`, "warning"},
		{"completed", `{"completed":0,"failed":3,"total":3}`, "warning"},
		{"completed", `{"completed":0,"failed":0,"total":0}`, ""},
		{"completed", `{"completed":2,"failed":1}`, ""},
		{"completed", `{"completed":null,"failed":1,"total":3}`, ""},
		{"completed", `{"completed":"2","failed":1,"total":3}`, ""},
		{"completed", `{"completed":1,"failed":1,"total":3}`, ""},
		{"completed", `{"completed":0,"failed":4,"total":3}`, ""},
		{"completed", `{"completed":18446744073709551615,"failed":2,"total":1}`, ""},
		{"completed", `{"completed":18446744073709551614,"failed":1,"total":18446744073709551615}`, "warning"},
		{"completed", `{"completed":18446744073709551615,"failed":0,"total":18446744073709551615}`, "positive"},
	} {
		batch := gjson.Parse(fmt.Sprintf(`{"status":%q,"request_counts":%s}`, tc.status, tc.counts))
		require.Equal(t, tc.tone, batchBrowserStatusTone(batch), "%s %s", tc.status, tc.counts)
	}
	for _, tc := range []struct{ counts, field, tone string }{
		{`{"completed":2}`, "completed", "positive"},
		{`{"failed":1}`, "failed", "negative"},
		{`{"total":3}`, "total", ""},
		{`{"completed":0,"failed":0}`, "completed", ""},
		{`{"completed":0,"failed":0}`, "failed", ""},
		{`{"completed":"3"}`, "completed", ""},
		{`{"failed":-1}`, "failed", ""},
		{`{}`, "failed", ""},
	} {
		batch := gjson.Parse(`{"request_counts":` + tc.counts + `}`)
		require.Equal(t, tc.tone, batchBrowserCountTone(batch, tc.field), tc.counts)
	}
}

// Read SGR state at visible text, including combined foreground, fill, and bold sequences.
func batchBrowserTestTextStyle(t *testing.T, text, target string) (foreground string, bold bool) {
	t.Helper()
	index := strings.Index(ansi.Strip(text), target)
	require.GreaterOrEqual(t, index, 0, "missing %q in %q", target, ansi.Strip(text))
	position := 0
	for text != "" {
		sequence, _, read := terminal.Sequence(text)
		text = text[read:]
		if strings.HasPrefix(sequence, "\x1b[") && strings.HasSuffix(sequence, "m") {
			parameters := strings.Split(sequence[2:len(sequence)-1], ";")
			for i := 0; i < len(parameters); i++ {
				switch parameters[i] {
				case "", "0":
					foreground, bold = "", false
				case "1":
					bold = true
				case "22":
					bold = false
				case "39":
					foreground = ""
				case "38", "48":
					if i+4 < len(parameters) && parameters[i+1] == "2" {
						if parameters[i] == "38" {
							foreground = strings.Join(parameters[i+2:i+5], ";")
						}
						i += 4
					} else if i+2 < len(parameters) && parameters[i+1] == "5" {
						i += 2
					}
				}
			}
		}
		visible := ansi.Strip(sequence)
		if position <= index && index < position+len(visible) {
			return foreground, bold
		}
		position += len(visible)
	}
	t.Fatal("target text had no style position")
	return "", false
}

func TestBatchBrowserSemanticColorsAndQuietLabels(t *testing.T) {
	for _, theme := range []struct{ background, green, amber, red, muted, selected string }{
		{"0", "141;201;160", "226;188;121", "229;154;154", "164;172;194", "242;244;250"},
		{"15", "35;107;67", "136;91;20", "178;58;58", "101;112;135", "32;40;59"},
	} {
		t.Run(theme.background, func(t *testing.T) {
			t.Setenv("COLORFGBG", theme.background)
			t.Setenv("NO_COLOR", "")
			t.Setenv("FORCE_COLOR", "")
			batch := gjson.Parse(`{"id":"batch_one","status":"completed","request_counts":{"completed":2,"failed":1,"total":3}}`)
			list := newBatchBrowserList(batchBrowsePage{items: []gjson.Result{batch, batch, batch}}, false)
			view := list.View().Content
			fg, bold := batchBrowserTestTextStyle(t, view, "3 batches")
			require.Equal(t, theme.muted, fg)
			require.True(t, bold, "batch count must be bold without another accent")
			for _, selected := range []bool{true, false} {
				list.searchFocused = !selected
				for _, width := range []int{80, 40} {
					list.width = width
					view = list.View().Content
					for _, expected := range []struct{ text, color string }{{"completed", theme.amber}, {"2/3", theme.green}} {
						fg, _ := batchBrowserTestTextStyle(t, view, expected.text)
						require.Equal(t, expected.color, fg, "width=%d selected=%v text=%s", width, selected, expected.text)
					}
					if width == 40 {
						fg, _ := batchBrowserTestTextStyle(t, view, "1  Succeeded")
						require.Equal(t, theme.red, fg)
						fg, _ = batchBrowserTestTextStyle(t, view, "Failed")
						require.Equal(t, theme.muted, fg)
					}
				}
			}
			actions := newBatchBrowserActions("batch_one")
			actions.overview = batch
			for _, width := range []int{80, 30} {
				actions.width = width
				view = actions.View().Content
				for _, expected := range []struct{ text, color string }{
					{"Completed", theme.amber}, {"succeeded", theme.muted}, {"2", theme.green},
					{"failed", theme.muted}, {"1", theme.red}, {"of", theme.muted}, {"3", theme.muted},
				} {
					fg, _ := batchBrowserTestTextStyle(t, view, expected.text)
					require.Equal(t, expected.color, fg, "width=%d text=%s", width, expected.text)
				}
			}
			for _, counts := range []string{`{"completed":0,"failed":0,"total":3}`, `{}`} {
				batch := gjson.Parse(`{"id":"batch_neutral","status":"completed","request_counts":` + counts + `}`)
				m := newBatchBrowserList(batchBrowsePage{items: []gjson.Result{batch}}, false)
				view := m.View().Content
				fg, _ := batchBrowserTestTextStyle(t, view, "completed")
				require.Equal(t, theme.selected, fg)
				value := "0/3"
				if counts == `{}` {
					value = "—"
				}
				fg, _ = batchBrowserTestTextStyle(t, view, value)
				require.Equal(t, theme.selected, fg, "neutral values retain selected text contrast")
			}
		})
	}
}

func TestBatchBrowserSemanticColorsPreservePlainLayout(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	batch := gjson.Parse(`{"id":"batch_one","status":"completed","request_counts":{"completed":2,"failed":1,"total":3}}`)
	for _, size := range [][2]int{{30, 7}, {40, 12}, {80, 24}} {
		for _, kind := range []string{"list", "actions"} {
			m := newBatchBrowserActions("batch_one")
			m.overview = batch
			if kind == "list" {
				m = newBatchBrowserList(batchBrowsePage{items: []gjson.Result{batch}}, false)
			}
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			colored := m.View().Content
			for _, mode := range []string{"NO_COLOR", "FORCE_COLOR"} {
				value := "1"
				if mode == "FORCE_COLOR" {
					value = "0"
				}
				t.Setenv(mode, value)
				plain := m.View().Content
				require.NotContains(t, plain, "38;2;", mode)
				require.NotContains(t, plain, "48;2;", mode)
				require.Equal(t, ansi.Strip(colored), ansi.Strip(plain), "%s %v %s", kind, size, mode)
				require.Len(t, strings.Split(ansi.Strip(plain), "\n"), size[1])
				for _, line := range strings.Split(ansi.Strip(plain), "\n") {
					require.LessOrEqual(t, ansi.StringWidth(line), size[0])
				}
				t.Setenv(mode, "")
			}
		}
	}
}

func TestBatchBrowserWrappedSummaryKeepsSemanticColors(t *testing.T) {
	for _, theme := range []struct{ background, green, red, muted string }{
		{"0", "141;201;160", "229;154;154", "164;172;194"},
		{"15", "35;107;67", "178;58;58", "101;112;135"},
	} {
		t.Run(theme.background, func(t *testing.T) {
			t.Setenv("COLORFGBG", theme.background)
			t.Setenv("NO_COLOR", "")
			t.Setenv("FORCE_COLOR", "")
			m := newBatchBrowserActions("batch_one")
			m.setOverview(gjson.Parse(`{"status":"completed","request_counts":{"completed":9223372036854775807,"total":18446744073709551615,"failed":9223372036854775808}}`))
			m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
			colored := m.View().Content
			for _, expected := range []struct{ text, color string }{
				{"18446744073709551615 succeeded", theme.muted},
				{"9223372036854775807", theme.green},
				{"9223372036854775808", theme.red},
				{"failed", theme.muted},
			} {
				fg, _ := batchBrowserTestTextStyle(t, colored, expected.text)
				require.Equal(t, expected.color, fg, expected.text)
			}
			for _, mode := range []string{"NO_COLOR", "FORCE_COLOR"} {
				value := "1"
				if mode == "FORCE_COLOR" {
					value = "0"
				}
				t.Setenv(mode, value)
				plain := m.View().Content
				require.NotContains(t, plain, "38;2;")
				require.NotContains(t, plain, "48;2;")
				require.Equal(t, ansi.Strip(colored), ansi.Strip(plain))
				t.Setenv(mode, "")
			}
		})
	}
}

func TestBatchBrowserErrorsChoiceRequiresExplicitSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, status, counts, file, diagnostics string
		hide                                    bool
	}{
		{"explicit null", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `null`, true},
		{"explicit empty", "completed", `{"completed":3,"failed":0,"total":3}`, `""`, `{"object":"list","data":[]}`, true},
		{"empty data", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":[]}`, true},
		{"missing diagnostics", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, "", false},
		{"missing file", "completed", `{"completed":3,"failed":0,"total":3}`, "", `null`, false},
		{"file exists", "completed", `{"completed":3,"failed":0,"total":3}`, `"file_errors"`, `null`, false},
		{"validation errors", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":[{"message":"invalid"}]}`, false},
		{"malformed string", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `"unknown"`, false},
		{"malformed array", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `[]`, false},
		{"missing data", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{}`, false},
		{"null data", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":null}`, false},
		{"wrong data", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":false}`, false},
		{"unknown field", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":[],"future":["error"]}`, false},
		{"unknown object", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":[],"object":"future"}`, false},
		{"duplicate data", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `{"data":[],"data":[{"message":"invalid"}]}`, false},
		{"duplicate errors", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `null,"errors":{"data":[{"message":"invalid"}]}`, false},
		{"duplicate status", "completed", `{"completed":3,"failed":0,"total":3}`, `null`, `null,"status":"failed"`, false},
		{"duplicate error file", "completed", `{"completed":3,"failed":0,"total":3}`, `null,"error_file_id":"file_errors"`, `null`, false},
		{"duplicate counts object", "completed", `{"completed":3,"failed":0,"total":3},"request_counts":{"completed":2,"failed":1,"total":3}`, `null`, `null`, false},
		{"duplicate succeeded", "completed", `{"completed":3,"completed":2,"failed":0,"total":3}`, `null`, `null`, false},
		{"duplicate failed", "completed", `{"completed":3,"failed":0,"failed":1,"total":3}`, `null`, `null`, false},
		{"duplicate total", "completed", `{"completed":3,"failed":0,"total":3,"total":4}`, `null`, `null`, false},
		{"invalid file", "completed", `{"completed":3,"failed":0,"total":3}`, `{}`, `null`, false},
		{"missing count", "completed", `{"completed":3,"total":3}`, `null`, `null`, false},
		{"invalid count", "completed", `{"completed":"3","failed":0,"total":3}`, `null`, `null`, false},
		{"inconsistent counts", "completed", `{"completed":2,"failed":0,"total":3}`, `null`, `null`, false},
		{"zero count", "completed", `{"completed":0,"failed":0,"total":0}`, `null`, `null`, false},
		{"active", "in_progress", `{"completed":3,"failed":0,"total":3}`, `null`, `null`, false},
		{"failed", "failed", `{"completed":3,"failed":0,"total":3}`, `null`, `null`, false},
		{"expired", "expired", `{"completed":3,"failed":0,"total":3}`, `null`, `null`, false},
		{"cancelled", "cancelled", `{"completed":3,"failed":0,"total":3}`, `null`, `null`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":"batch_one","status":%q,"request_counts":%s`, tc.status, tc.counts)
			if tc.file != "" {
				body += `,"error_file_id":` + tc.file
			}
			if tc.diagnostics != "" {
				body += `,"errors":` + tc.diagnostics
			}
			m := newBatchBrowserActions("batch_one")
			m.setOverview(gjson.Parse(body + `}`))
			values := []string{}
			for _, choice := range m.choices {
				values = append(values, choice.value)
			}
			require.Equal(t, !tc.hide, strings.Contains(strings.Join(values, ","), "error"))
			for _, value := range []string{"output", "input", "download", "status"} {
				require.Contains(t, values, value)
			}
			m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
			for i := range m.choices {
				m.index = i
				view := ansi.Strip(m.View().Content)
				require.Len(t, strings.Split(view, "\n"), 7, view)
				require.Contains(t, view, "> "+m.choices[i].title)
				choice := m.choices[i]
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				if choice.value == "download" {
					require.True(t, m.saveExpanded)
					require.False(t, m.closed)
					m.setSaveExpanded(false)
				} else {
					require.Equal(t, choice.value, m.chosen)
				}
				m.Init()
			}
		})
	}
}

func TestBatchBrowserOverviewPreservesActionByValue(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	knownSuccess := gjson.Parse(`{"status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null,"errors":null}`)
	for _, action := range []string{"output", "input", "download", "status"} {
		m.setOverview(gjson.Result{})
		for i, choice := range m.choices {
			if choice.value == action {
				m.index = i
			}
		}
		m.setOverview(knownSuccess)
		require.Equal(t, action, m.choices[m.visible[m.index]].value)
		m.setOverview(gjson.Result{})
		require.Equal(t, action, m.choices[m.visible[m.index]].value)
	}
	m.index = 1
	require.Equal(t, "error", m.choices[m.index].value)
	m.setOverview(knownSuccess)
	require.Equal(t, "output", m.choices[m.visible[m.index]].value, "a removed action returns to View results")
}

func TestBatchBrowserCompactSummaryPreservesUncertainty(t *testing.T) {
	render := func(text, _ string) string { return text }
	for _, tc := range []struct{ batch, want string }{
		{`{"status":"completed","request_counts":{"completed":3,"failed":0,"total":3}}`, "Completed  3 of 3 succeeded"},
		{`{"status":"completed","request_counts":{"completed":2,"failed":1,"total":3}}`, "Completed  2 of 3 succeeded  1 failed"},
		{`{"status":"in_progress","request_counts":{"completed":1,"failed":0,"total":3}}`, "In progress  1 of 3 succeeded"},
		{`{"status":"completed","request_counts":{"completed":2,"failed":0,"total":3}}`, "Completed  Succeeded 2  Failed 0  Total 3"},
		{`{"status":"validating","request_counts":{"completed":null,"failed":"0"}}`, "Validating  Succeeded —  Failed —  Total —"},
		{`{"status":"future_CUSTOM","request_counts":{"completed":0,"failed":0,"total":3}}`, "future_CUSTOM  0 of 3 succeeded"},
	} {
		m := newBatchBrowserActions("batch_one")
		m.setOverview(gjson.Parse(tc.batch))
		require.Equal(t, tc.want, m.overviewText(" ", 100, render))
		require.Equal(t, "batch_one", m.title)
	}
	for _, counts := range []string{
		`{"completed":18446744073709551615,"failed":0,"total":18446744073709551615}`,
		`{"completed":9223372036854775808,"failed":9223372036854775807,"total":18446744073709551615}`,
	} {
		m := newBatchBrowserActions("batch_one")
		m.setOverview(gjson.Parse(`{"status":"completed","request_counts":` + counts + `}`))
		m.Update(tea.WindowSizeMsg{Width: 30, Height: 7})
		for i := range m.choices {
			m.index = i
			view := ansi.Strip(m.View().Content)
			require.Len(t, strings.Split(view, "\n"), 7, view)
			require.Contains(t, view, "18446744073709551615")
			require.Contains(t, view, "> "+m.choices[i].title)
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, ansi.StringWidth(line), 30, line)
			}
		}
	}
}

func TestBatchBrowserActionsGroupIdentityAndOutcome(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	m.setOverview(gjson.Parse(`{"status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null,"errors":null}`))
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	require.Contains(t, lines[1], "batch_one")
	require.Contains(t, lines[2], "Completed  3 of 3 succeeded")
	require.NotEmpty(t, strings.Trim(lines[3], "│ "))
	require.Empty(t, strings.Trim(lines[3], "│ ─"))
	require.Contains(t, lines[4], "> View results", "actions follow the divider without a blank row")
}

func TestBatchBrowserActionHintsFollowFilesAndWidth(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")
	t.Setenv("COLORFGBG", "0")
	batch := gjson.Parse(`{"status":"completed","endpoint":"/v1/responses","output_file_id":"file_out","input_file_id":"file_in","error_file_id":"file_err","request_counts":{"total":3,"completed":2,"failed":1}}`)
	m := newBatchBrowserActions("batch_partial_demo")
	m.setOverview(batch)
	view := m.View().Content
	for _, value := range []string{"Output ready", "Error file ready", "Request JSONL", "/v1/responses"} {
		require.Contains(t, ansi.Strip(view), value)
	}
	fg, _ := batchBrowserTestTextStyle(t, view, "Error file ready")
	require.Equal(t, "229;154;154", fg)
	fg, _ = batchBrowserTestTextStyle(t, view, "Output ready")
	require.Equal(t, "141;201;160", fg)
	t.Setenv("NO_COLOR", "1")
	require.Equal(t, ansi.Strip(view), m.View().Content)
	m.width = 40
	require.NotContains(t, m.View().Content, "Output ready")
	require.Contains(t, m.View().Content, "> View results")
	m.width = 80
	m.setOverview(gjson.Parse(`{"status":"in_progress","output_file_id":null}`))
	require.Contains(t, m.View().Content, "No file yet")
	m.setOverview(gjson.Parse(`{"status":"completed","error_file_id":null,"errors":{"data":[{"code":"invalid"}]}}`))
	require.Contains(t, m.View().Content, "Check diagnostics")
}

func TestBatchBrowserActionsShowsAllChoicesAt50x12(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	m.setOverview(gjson.Parse(`{"status":"completed","request_counts":{"completed":2,"failed":1,"total":3},"error_file_id":"file_errors","errors":null}`))
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 12})
	view := ansi.Strip(m.View().Content)
	lines := strings.Split(view, "\n")
	require.Len(t, lines, 12, view)
	for _, choice := range m.choices {
		require.Contains(t, view, choice.title)
	}
	for _, line := range lines[1 : len(lines)-1] {
		require.True(t, strings.HasPrefix(line, "│"), line)
		require.True(t, strings.HasSuffix(line, "│"), line)
		require.Equal(t, 50, ansi.StringWidth(line), line)
	}
	require.Contains(t, view, "Ctrl+R refresh")
	require.Contains(t, view, "Esc back")
	require.Empty(t, strings.Trim(lines[len(lines)-3], "│ "), "preserve the footer's blank row")
}

func TestBatchBrowserInlineSaveNavigation(t *testing.T) {
	m := newBatchBrowserActions("batch_one")
	m.setOverview(gjson.Parse(`{"status":"completed","request_counts":{"completed":3,"failed":0,"total":3},"error_file_id":null,"errors":null}`))
	require.Len(t, m.choices, 4)
	m.index = len(m.choices) - 1
	require.Equal(t, "download", m.choices[m.index].value)
	require.Contains(t, ansi.Strip(m.View().Content), "Enter expand")
	_, command := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Nil(t, command, "expansion must not dispatch a controller action")
	require.Empty(t, m.chosen)
	require.False(t, m.closed)
	require.Len(t, m.choices, 7)
	require.Equal(t, "save:output", m.choices[m.index].value)
	require.Contains(t, ansi.Strip(m.View().Content), ">   Output file")
	require.Contains(t, ansi.Strip(m.View().Content), "Esc collapse")
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, "save:output", m.choices[m.index].value)
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "save:input", m.choices[m.index].value)
	// Refresh may add View errors. Keep the expanded child selected by value.
	m.setOverview(gjson.Parse(`{"status":"in_progress"}`))
	require.True(t, m.saveExpanded)
	require.Equal(t, "save:input", m.choices[m.index].value)
	_, command = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Nil(t, command)
	require.False(t, m.saveExpanded)
	require.False(t, m.closed)
	require.Equal(t, "download", m.choices[m.index].value)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.True(t, m.saveExpanded)
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.False(t, m.saveExpanded)
	require.Equal(t, "download", m.choices[m.index].value)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, m.closed)
	require.Equal(t, "back", m.chosen)
}

func TestBatchBrowserInlineSaveChildrenRemainReachable(t *testing.T) {
	for _, size := range [][2]int{{30, 7}, {50, 12}, {90, 26}} {
		for _, selection := range []string{"save:output", "save:error", "save:input"} {
			t.Run(fmt.Sprint(size)+selection, func(t *testing.T) {
				m := newBatchBrowserActions("batch_one")
				m.setSaveExpanded(true)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				for i, choice := range m.choices {
					if choice.value == selection {
						m.index = i
					}
				}
				view := ansi.Strip(m.View().Content)
				require.Contains(t, view, ">   "+m.choices[m.index].title)
				require.LessOrEqual(t, len(strings.Split(view, "\n")), size[1])
				for _, line := range strings.Split(view, "\n") {
					require.LessOrEqual(t, ansi.StringWidth(line), size[0])
				}
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.True(t, m.closed)
				require.Equal(t, selection, m.chosen)
			})
		}
	}
}

func TestBatchBrowserInlinePathKeepsFileChoiceWhileEditing(t *testing.T) {
	actions := newBatchBrowserActions("batch_one")
	actions.setSaveExpanded(true)
	path := newBatchBrowserPath(actions, "input")
	require.Equal(t, "batch_one-input.jsonl", path.input.Value())
	path.input.SetValue("")
	selected := path.index
	path.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	path.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	path.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	path.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.Equal(t, "axb", path.input.Value())
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyUp}, {Code: tea.KeyDown}, {Code: tea.KeyTab},
		{Code: tea.KeyTab, Mod: tea.ModShift}, {Code: 'r', Mod: tea.ModCtrl},
	} {
		_, command := path.Update(key)
		require.Nil(t, command)
		require.Equal(t, selected, path.index)
		require.Equal(t, "save:input", path.choices[path.index].value)
		require.Empty(t, path.chosen)
	}
	require.Equal(t, "save:output", actions.choices[actions.index].value)
	require.Empty(t, actions.input.Value())
	path.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, "back", path.chosen)
	require.Equal(t, "axb", path.input.Value())
	require.False(t, actions.closed)
}

func TestBatchBrowserEmptyPathReturnsToNavigation(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: tea.KeyUp}, {Code: tea.KeyDown}, {Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift},
	} {
		for _, value := range []string{"", "  "} {
			t.Run(key.String()+value, func(t *testing.T) {
				m := newBatchBrowserPath(newBatchBrowserActions("batch_one"), "output")
				m.input.SetValue(value)
				m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				require.NotEmpty(t, m.notice)
				require.Contains(t, ansi.Strip(m.View().Content), "↑↓ move")
				_, command := m.Update(key)
				require.NotNil(t, command)
				require.Equal(t, "back", m.chosen)
				require.True(t, m.closed)
				require.Empty(t, m.notice)
				selected := "save:error"
				if key.Code == tea.KeyUp || key.Mod == tea.ModShift {
					selected = "download"
				}
				require.Equal(t, selected, m.choices[m.visible[m.index]].value)
			})
		}
	}
}

func TestBatchBrowserInlinePathFitsSelectedFileAndControls(t *testing.T) {
	for _, size := range [][2]int{{30, 7}, {50, 12}, {90, 26}} {
		for _, selection := range []string{"output", "error", "input"} {
			t.Run(fmt.Sprint(size)+selection, func(t *testing.T) {
				actions := newBatchBrowserActions("batch_one")
				m := newBatchBrowserPath(actions, selection)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.input.SetValue(strings.Repeat("界😀", 30) + ".jsonl")
				for _, invalid := range []bool{false, true} {
					if invalid {
						m.input.SetValue("-")
						m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
						require.False(t, m.closed)
					}
					view := ansi.Strip(m.View().Content)
					lines := strings.Split(view, "\n")
					require.LessOrEqual(t, len(lines), size[1], view)
					for i, line := range lines {
						require.LessOrEqual(t, ansi.StringWidth(line), size[0], line)
						if strings.Contains(line, ">   "+m.choices[m.index].title) {
							require.Contains(t, lines[i+1], "File:")
						}
					}
					for _, text := range []string{"batch_one", ">   " + m.choices[m.index].title, "File:", "Enter save", "Esc back"} {
						require.Contains(t, view, text)
					}
					require.NotContains(t, view, "Ctrl+R")
				}
			})
		}
	}
}
