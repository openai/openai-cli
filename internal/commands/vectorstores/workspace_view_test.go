package vectorstores

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFullScreenKeepsDraftAndBottomShortcuts(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 34})
	m.enter("home")
	m.Update(tea.PasteMsg{Content: "moonflower walk"})
	compact := m.View().Content
	require.Contains(t, ansi.Strip(compact), "Ctrl+G expand")
	modelKey(m, tea.KeyF2)
	require.False(t, m.fullScreen)
	toggle := tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}
	_, cmd := m.Update(toggle)
	require.Nil(t, cmd)
	m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl, IsRepeat: true})
	for _, size := range []tea.WindowSizeMsg{{Width: 200, Height: 34}, {Width: 40, Height: 12}, {Width: 30, Height: 8}} {
		m.Update(size)
		rows := strings.Split(ansi.Strip(m.View().Content), "\n")
		require.Len(t, rows, size.Height)
		require.True(t, strings.HasSuffix(rows[len(rows)-1], "Ctrl+G compact"))
		for _, row := range rows {
			require.Less(t, ansi.StringWidth(row), size.Width)
		}
		if size.Width >= 40 {
			require.Contains(t, strings.Join(rows[len(rows)-len(m.footerLines(m.searchFooter())):], " "), "Enter search")
		}
		if size.Width == 200 {
			require.Equal(t, size.Width-1, ansi.StringWidth(rows[0]))
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 200, Height: 34})
	_, cmd = m.Update(toggle)
	require.Nil(t, cmd)
	require.Equal(t, "moonflower walk", m.editor.Value())
	require.Equal(t, compact, m.View().Content)
}

func TestCtrlGTogglesFromListsFieldsAndActions(t *testing.T) {
	m := modelForTest(t, modelService{})
	z := tea.KeyPressMsg{Code: 'z', Text: "z"}
	ctrlG := tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl, Text: "g"}
	m.enter("stores")
	m.Update(z)
	require.False(t, m.fullScreen)
	require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+G expand")
	m.Update(ctrlG)
	require.True(t, m.fullScreen)
	require.Contains(t, ansi.Strip(m.View().Content), "Ctrl+G compact")

	m.focus = storeFinderFocus
	m.Update(ctrlG)
	require.False(t, m.fullScreen)
	require.Equal(t, "stores", m.page)
	m.Update(z)
	require.Equal(t, "filter", m.page)
	require.Equal(t, "z", m.editor.Value())

	m.enter("home")
	m.Update(z)
	m.Update(ctrlG)
	require.True(t, m.fullScreen)
	require.Equal(t, "z", m.editor.Value())
	modelKey(m, tea.KeyTab)
	m.Update(z)
	require.True(t, m.fullScreen)
	m.Update(ctrlG)
	require.False(t, m.fullScreen)
	require.Equal(t, "z", m.editor.Value())
}

func TestWorkspaceFrameRetainsFocusedControls(t *testing.T) {
	pages := []string{"stores", "create", "home", "upload", "files", "batchfiles", "browse", "manage", "file", "batch", "sources", "filter", "rename", "expiration", "metadata", "attributes", "attach-options", "open-batch", "delete-store", "remove-file", "cancel-batch"}
	for _, size := range [][2]int{{40, 12}, {60, 16}, {90, 34}, {140, 40}} {
		for _, page := range pages {
			t.Run(fmt.Sprintf("%s/%dx%d", page, size[0], size[1]), func(t *testing.T) {
				m := modelForTest(t, modelService{})
				m.store = &openai.VectorStore{ID: "vs_synthetic", Name: "参考 synthetic store"}
				m.file = &openai.VectorStoreFile{ID: "file_synthetic", Status: "completed"}
				m.batch = &openai.VectorStoreFileBatch{ID: "vsfb_synthetic", Status: "completed"}
				for i := 0; i < 20; i++ {
					m.stores = append(m.stores, openai.VectorStore{ID: fmt.Sprint(i), Name: "synthetic store"})
					m.files = append(m.files, *m.file)
					m.sourceFiles = append(m.sourceFiles, openai.FileObject{ID: fmt.Sprint(i), Filename: "synthetic.txt"})
					m.entries = append(m.entries, browserEntry{name: "synthetic.txt"})
				}
				m.enter(page)
				m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				m.note = "Synthetic feedback that wraps over multiple lines in small windows."
				m.selected = max(0, m.listLength()-1)
				if m.editing() && page != "filter" {
					m.focus = 1
					m.syncFocus()
				}
				for _, theme := range []string{"dark", "light", "mono"} {
					m.color, m.dark = theme != "mono", theme == "dark"
					view := m.View().Content
					lines := strings.Split(view, "\n")
					assert.Less(t, len(lines), m.height, theme)
					width := m.bodyWidth()
					plain := strings.Split(ansi.Strip(view), "\n")
					for _, line := range plain {
						assert.Less(t, ansi.StringWidth(line), m.width)
					}
					if m.framed() {
						assert.Equal(t, "╭"+strings.Repeat("─", width+2)+"╮", plain[0])
						bottom := "╰" + strings.Repeat("─", width+2) + "╯"
						bottomRow := -1
						for i, line := range plain {
							if line == bottom {
								bottomRow = i
								break
							}
						}
						require.Greater(t, bottomRow, 1)
						for _, line := range plain[1:bottomRow] {
							assert.True(t, strings.HasPrefix(line, "│ "))
							assert.True(t, strings.HasSuffix(line, " │"))
							assert.Equal(t, width+4, ansi.StringWidth(line))
						}
						assert.Greater(t, len(plain), bottomRow+1)
						assert.Contains(t, strings.Join(plain[bottomRow+1:], " "), "Esc")
					} else {
						assert.NotContains(t, view, "╭")
					}
					assert.Contains(t, ansi.Strip(view), "›", "focused control must remain visible: %s", theme)
					assert.Contains(t, ansi.Strip(view), "Esc", "back must remain visible: %s", theme)
					if theme == "mono" {
						leftWidth, _ := m.paneWidths()
						withoutPositioning := strings.ReplaceAll(view, ansi.CursorHorizontalAbsolute(leftWidth+4), "")
						if m.editing() {
							// The editor uses reverse video for the cursor in monochrome.
							withoutPositioning = strings.NewReplacer("\x1b[7m", "", "\x1b[m", "").Replace(withoutPositioning)
						}
						assert.NotContains(t, withoutPositioning, "\x1b", "monochrome must omit color escapes")
					}
				}
			})
		}
	}
}

func TestWorkspacePaneDividerPinsStrokeAndRestoresText(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width, m.height = 120, 32
	leftWidth, _ := m.paneWidths()
	anchor := ansi.CursorHorizontalAbsolute(leftWidth + 4)
	enter, leave := ansi.SelectCharacterSet('(', '0'), ansi.SelectCharacterSet('(', 'B')
	for _, dec := range []bool{false, true} {
		m.decLineDrawing = dec
		stroke := "│"
		if dec {
			stroke = enter + "x" + leave
		}
		rows := m.appendPanes(nil, []string{"🔎 search", "files"}, []string{"passage", "status"}, "")
		for i, right := range []string{"passage", "status"} {
			left, after, found := strings.Cut(rows[i], anchor)
			require.True(t, found)
			assert.Equal(t, leftWidth+1, ansi.StringWidth(left))
			assert.Equal(t, stroke+" "+right, after)
		}
		inline := workspaceInline{model: m, modesActive: true}
		assert.Equal(t, dec, strings.Contains(inline.close(false), leave))
	}
}

func TestExpandedPaneDividerFillsBody(t *testing.T) {
	for _, page := range []string{"stores", "filter", "home", "results", "upload", "browse", "create"} {
		t.Run(page, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.fullScreen = true
			m.store = &openai.VectorStore{ID: "vs_demo", Name: "Demo"}
			if page == "results" {
				m.enter("home")
				m.results = []openai.VectorStoreSearchResponse{{FileID: "file_demo", Filename: "guide.txt"}}
				m.focus = matchListFocus
			} else {
				m.enter(page)
			}
			m.note = "First feedback row.\nSecond feedback row."
			for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 32}, {Width: 80, Height: 24}, {Width: 96, Height: 16}} {
				m.Update(size)
				rows := strings.Split(m.View().Content, "\n")
				require.Len(t, rows, size.Height)
				bottom := -1
				for i, row := range rows {
					if strings.HasPrefix(ansi.Strip(row), "╰") {
						bottom = i
						break
					}
				}
				const firstPaneRow = 5 // Top border, title, rule, and two feedback rows.
				require.Greater(t, bottom, firstPaneRow)
				require.Contains(t, ansi.Strip(rows[3]), "First feedback row.")
				require.Contains(t, ansi.Strip(rows[4]), "Second feedback row.")
				leftWidth, _ := m.paneWidths()
				anchor := ansi.CursorHorizontalAbsolute(leftWidth + 4)
				for i, row := range rows {
					want := size.Width >= 96 && page != "create" && i >= firstPaneRow && i < bottom
					assert.Equal(t, want, strings.Contains(row, anchor), "%dx%d row %d", size.Width, size.Height, i)
				}
				require.Contains(t, ansi.Strip(rows[len(rows)-1]), "compact")
			}
		})
	}
}

func TestWorkspaceResultAndSourceTablesAlignMetadata(t *testing.T) {
	for _, width := range []int{40, 60, 90, 140} {
		m := modelForTest(t, modelService{})
		m.width = width
		m.results = []openai.VectorStoreSearchResponse{
			{Filename: strings.Repeat("参考", 30), Score: .9},
			{Filename: "short.txt", Score: .123},
		}
		columns, rows := m.resultTable(m.bodyWidth())
		first := ansi.Strip(m.tableRow(columns, rows[0], true))
		second := ansi.Strip(m.tableRow(columns, rows[1], false))
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(first))
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(second))
		assert.True(t, strings.HasSuffix(first, "0.900"))
		assert.True(t, strings.HasSuffix(second, "0.123"))
		m.sourceFiles = []openai.FileObject{{ID: "file_selected", Filename: strings.Repeat("参考", 30)}}
		m.selectedSources = map[string]bool{"file_selected": true}
		columns, rows = m.sourceTable()
		row := ansi.Strip(m.tableRow(columns, rows[0], true))
		assert.Contains(t, row, "[x]")
		assert.Contains(t, row, "file_selected")
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(row))
	}
}

func TestWorkspaceFileTableDoesNotRepeatFallbackIDs(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width = 110
	m.files = []openai.VectorStoreFile{{ID: "file_first", Status: "completed"}, {ID: "file_second", Status: "failed"}}
	for _, known := range []bool{false, true} {
		if known {
			m.names["file_first"] = "known.txt"
		}
		columns, rows := m.fileTable()
		for i, file := range m.files {
			row := ansi.Strip(m.tableRow(columns, rows[i], i == 0))
			assert.Equal(t, 1, strings.Count(row, file.ID))
		}
	}
}

func TestWorkspaceCompactSourceTableKeepsFullFileID(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width = 60
	id := "file-0123456789abcdefghijklmn"
	m.sourceFiles = []openai.FileObject{{ID: id, Filename: "short.txt"}}
	columns, rows := m.sourceTable()
	row := ansi.Strip(m.tableRow(columns, rows[0], true))
	assert.Contains(t, row, "[ ] short.txt")
	assert.Contains(t, row, id)
	assert.Equal(t, m.bodyWidth(), ansi.StringWidth(row))
}

func TestWorkspaceDetailsPreserveValuesAcrossResizeAndContentViews(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.color, m.dark = true, true
	value := strings.Repeat("参考", 50) + "final value"
	m.openDetails("File details", []detailField{{"ID", value}, {"Status", "failed"}}, "file")
	for _, width := range []int{90, 40, 60, 140} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		for _, line := range m.passageLines {
			assert.LessOrEqual(t, ansi.StringWidth(line), m.bodyWidth())
		}
		plain := ansi.Strip(strings.Join(m.passageLines, "\n"))
		assert.Contains(t, plain, "final value")
		assert.Contains(t, strings.Join(m.passageLines, "\n"), "38;2;229;154;154")
	}
	m.openDocument("Indexed file content", "Status: failed\nuser content", "file")
	assert.Equal(t, []string{"Status: failed", "user content"}, m.passageLines)
	assert.Empty(t, m.documentFields, "file content must not inherit structured detail styling")
}

func TestWorkspaceDividerUsesNeutralFadeWithoutChangingWidth(t *testing.T) {
	for _, width := range []int{40, 60, 90, 140} {
		m := modelForTest(t, modelService{})
		m.width, m.color, m.dark = width, true, true
		divider := m.divider()
		assert.Equal(t, strings.Repeat("─", m.bodyWidth()), ansi.Strip(divider))
		assert.Contains(t, divider, "38;2;52;61;88")
		assert.Contains(t, divider, "38;2;164;172;194")
		m.color = false
		assert.Equal(t, strings.Repeat("─", m.bodyWidth()), m.divider())
	}
}

func TestWorkspaceSourceCountExcludesDeselectedFiles(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.page = "sources"
	m.selectedSources = map[string]bool{"file_selected": true, "file_deselected": false}
	assert.Contains(t, m.View().Content, "1 selected")
}

func TestWorkspaceFormsDoNotRepeatTheirLabels(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_demo"}
	m.file = &openai.VectorStoreFile{ID: "file_demo"}
	m.batch = &openai.VectorStoreFileBatch{ID: "vsfb_demo"}
	for page, label := range managementForms {
		m.enter(page)
		m.editor.SetValue("")
		view := ansi.Strip(m.View().Content)
		assert.Equal(t, 1, strings.Count(view, label), page)
		assert.NotEmpty(t, m.editor.Placeholder, page)
	}
}

func TestWorkspaceInputTailSurvivesLayoutAndResize(t *testing.T) {
	m := modelForTest(t, modelService{})
	for _, page := range []string{"home", "create", "upload"} {
		m.enter(page)
		m.Update(tea.PasteMsg{Content: strings.Repeat("x", 70) + "TAIL"})
		for _, width := range []int{90, 40, 120, 60} {
			m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
			assert.Contains(t, m.View().Content, "TAIL", "%s at %d columns", page, width)
		}
	}
}

func TestWorkspaceTableKeepsStatusAndCountAligned(t *testing.T) {
	for _, width := range []int{90, 140} {
		m := modelForTest(t, modelService{})
		m.width = width
		m.stores = []openai.VectorStore{
			{ID: "vs_0123456789abcdef0123456789abcdef", Name: strings.Repeat("参考", 30), Status: "completed"},
			{ID: "vs_fedcba9876543210fedcba9876543210", Name: "short", Status: "in_progress"},
		}
		m.stores[0].FileCounts.Total = 7
		m.stores[1].FileCounts.Total = 12
		m.focus = 0
		columns, rows := m.storeTable()
		header := ansi.Strip(m.tableHeader(columns))
		first := ansi.Strip(m.tableRow(columns, rows[0], true))
		second := ansi.Strip(m.tableRow(columns, rows[1], false))
		statusStart := ansi.StringWidth(strings.Split(header, "Status")[0])
		assert.Equal(t, statusStart, ansi.StringWidth(strings.Split(first, "completed")[0]))
		assert.Equal(t, statusStart, ansi.StringWidth(strings.Split(second, "in_progress")[0]))
		assert.Contains(t, first, "    7")
		assert.Contains(t, second, "   12")
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(first))
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(second))
		if width >= 90 {
			assert.Contains(t, first, m.stores[0].ID)
		}
	}
}

func TestWorkspaceCompactStoreTablePreservesIdentity(t *testing.T) {
	for _, width := range []int{60, 80} {
		m := modelForTest(t, modelService{})
		m.width = width
		m.stores = []openai.VectorStore{
			{ID: "vs_0123456789abcdef0123456789abcdef", Name: "demo", Status: "completed"},
			{ID: "vs_fedcba9876543210fedcba9876543210", Name: "demo", Status: "completed"},
		}
		m.focus = 0
		view := ansi.Strip(m.View().Content)
		for _, store := range m.stores {
			assert.Contains(t, view, store.ID, "duplicate names retain their distinguishing ID at %d columns", width)
		}
	}
}

func TestWorkspaceFileStatusSurvivesNarrowLongNames(t *testing.T) {
	for _, width := range []int{40, 60, 80} {
		m := modelForTest(t, modelService{})
		m.width = width
		m.files = []openai.VectorStoreFile{{ID: "file_example", Status: "in_progress"}}
		m.names["file_example"] = strings.Repeat("参考", 40)
		columns, rows := m.fileTable()
		row := ansi.Strip(m.tableRow(columns, rows[0], true))
		assert.Contains(t, row, "in_progress")
		assert.Equal(t, m.bodyWidth(), ansi.StringWidth(row))
	}
}

func TestWorkspaceSelectionAndStatusPalette(t *testing.T) {
	for _, dark := range []bool{false, true} {
		m := modelForTest(t, modelService{})
		m.color, m.dark = true, dark
		m.files = []openai.VectorStoreFile{{ID: "file_example", Status: "completed"}}
		columns, rows := m.fileTable()
		selected := m.tableRow(columns, rows[0], true)
		text, fill, success, warning, failure := "38;2;32;40;59", "48;2;233;239;254", "38;2;35;107;67", "38;2;136;91;20", "38;2;178;58;58"
		if dark {
			text, fill, success, warning, failure = "38;2;242;244;250", "48;2;52;61;88", "38;2;141;201;160", "38;2;226;188;121", "38;2;229;154;154"
		}
		assert.Contains(t, selected, text)
		assert.Contains(t, selected, fill)
		assert.Contains(t, selected, success)
		assert.True(t, strings.HasPrefix(selected, m.accent().Inherit(m.selection()).Render("› ")))
		for _, status := range []string{"in_progress", "cancelled", "expired"} {
			m.files[0].Status = openai.VectorStoreFileStatus(status)
			_, rows = m.fileTable()
			assert.Contains(t, m.tableRow(columns, rows[0], true), warning)
		}
		m.files[0].Status = "failed"
		_, rows = m.fileTable()
		assert.Contains(t, m.tableRow(columns, rows[0], true), failure)
		assert.Equal(t, "unrecognized", m.status("unrecognized"))
		m.color = false
		assert.NotContains(t, m.tableRow(columns, rows[0], true), "\x1b")
		assert.Contains(t, m.tableRow(columns, rows[0], true), "failed")
	}
}

func TestFormAndFileFootersFollowTab(t *testing.T) {
	for _, tc := range []struct{ page, first string }{
		{"create", "Create store"}, {"upload", "Browse files"}, {"browse", "Parent folder"},
		{"files", "Upload file"}, {"batchfiles", "Refresh files"}, {"sources", "Attach selected"},
		{"rename", "Save"}, {"delete-store", "Delete store"},
	} {
		t.Run(tc.page, func(t *testing.T) {
			m := modelForTest(t, modelService{})
			m.width, m.height = 40, 12
			m.store = &openai.VectorStore{ID: "vs_demo"}
			m.files = []openai.VectorStoreFile{{ID: "file_demo", Status: "completed"}}
			m.enter(tc.page)
			require.Contains(t, ansi.Strip(m.View().Content), "Tab actions")
			modelKey(m, tea.KeyTab)
			view := ansi.Strip(m.View().Content)
			assert.Contains(t, view, "› "+tc.first)
			assert.Contains(t, view, "Tab next")
			assert.False(t, m.busy)
		})
	}
}

func TestWorkspaceCompactFooterWraps(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width, m.height = 40, 12
	m.enter("files")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	assert.Contains(t, lines[len(lines)-1], "Ctrl+C exit")
	assert.NotContains(t, strings.Join(lines, "\n"), "╰")
	assert.Less(t, len(lines), m.height)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Esc back") {
			found = true
			assert.True(t, strings.HasPrefix(line, "  "))
		}
		assert.Less(t, ansi.StringWidth(line), m.width)
	}
	require.True(t, found)
}

func TestStoreInspectorPrioritizesStatusAndPreservesID(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width, m.height = 96, 16
	store := openai.VectorStore{ID: "vs_" + strings.Repeat("a", 65), Status: "completed"}
	store.FileCounts.Total = 3
	for _, theme := range []string{"dark", "light", "mono"} {
		m.color, m.dark = theme != "mono", theme == "dark"
		lines := m.storeInspector(&store)
		require.Greater(t, len(lines), 4)
		assert.Equal(t, m.muted().Render("Status  ")+m.status("completed"), lines[1])
		assert.Equal(t, "Files   3", ansi.Strip(lines[2]))
		var id strings.Builder
		for _, line := range lines[3:] {
			value := strings.TrimSpace(strings.TrimPrefix(ansi.Strip(line), "ID "))
			id.WriteString(value)
		}
		assert.Equal(t, store.ID, id.String())
	}
}

func TestStoreFilterTitleUsesProjectScope(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.store = &openai.VectorStore{ID: "vs_before", Name: "Previously open"}
	m.enter("filter")
	assert.Equal(t, "Vector stores", ansi.Strip(m.header()))
}

func TestStoreListFailureShowsRecoveryBeforeDiagnostics(t *testing.T) {
	m := modelForTest(t, modelService{})
	err := errors.New("private upstream message")
	m.errorMessage = func(error) string {
		return "Couldn't sign in to OpenAI.\nReopen with your project API key.\n\nHTTP 401: Unauthorized.\nRequest ID: req_demo"
	}
	m.complete(workMessage{kind: "stores", err: err})
	require.ErrorIs(t, m.lastErr, err)
	for _, size := range [][2]int{{40, 12}, {120, 32}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		rows := m.feedbackLines()
		require.GreaterOrEqual(t, len(rows), 2)
		assert.Equal(t, "Couldn't sign in to OpenAI.", rows[0])
		assert.Equal(t, "Reopen with your project API key.", rows[1])
		view := ansi.Strip(m.View().Content)
		assert.Contains(t, view, "Reopen with your project API key.")
		assert.NotContains(t, view, "Could not load stores.")
		assert.Contains(t, view, "› Refresh stores")
		assert.NotContains(t, view, "0 loaded")
		assert.NotContains(t, view, `\n`)
		assert.NotContains(t, view, err.Error())
		if size[0] == 120 {
			assert.Len(t, rows, 2)
			assert.Contains(t, view, "Error details")
			assert.Equal(t, 1, strings.Count(view, "HTTP 401: Unauthorized."))
			assert.Contains(t, view, "Request ID: req_demo")
		}
	}
	m.complete(workMessage{kind: "stores", reset: true, stores: StorePage{
		Stores: []openai.VectorStore{{ID: "vs_known", Name: "Known store"}},
	}})
	require.NoError(t, m.lastErr)
	assert.Zero(t, m.focus)
	m.complete(workMessage{kind: "stores", err: err})
	view := ansi.Strip(m.View().Content)
	assert.Contains(t, view, "Known store")
	assert.Contains(t, view, "1 loaded")
}

func TestFileStatusCannotInsertAnotherFeedbackLine(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.complete(workMessage{kind: "watch", attached: &openai.VectorStoreFile{
		ID: "file_demo\nforged", Status: "completed",
	}})
	assert.Equal(t, []string{"File indexed. Ready to search.", `file_demo\nforged`}, m.feedbackLines())
}
