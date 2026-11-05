package vectorstores

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
				if m.editing() {
					m.focus = 1
					m.syncFocus()
				}
				for _, theme := range []string{"dark", "light", "mono"} {
					m.color, m.dark = theme != "mono", theme == "dark"
					view := m.View().Content
					lines := strings.Split(view, "\n")
					assert.Less(t, len(lines), m.height, theme)
					width := m.bodyWidth()
					border := m.muted()
					assert.Equal(t, " "+m.accent().Render("┌──")+border.Render(strings.Repeat("─", width)+"┐"), lines[0], theme)
					assert.Equal(t, " "+border.Render("└"+strings.Repeat("─", width+2)+"┘"), lines[len(lines)-1], theme)
					edge := border.Render("│")
					for i, line := range lines[1 : len(lines)-1] {
						left := edge
						if i == 0 {
							left = m.accent().Render("│")
						}
						assert.True(t, strings.HasPrefix(line, " "+left+" "), theme)
						assert.True(t, strings.HasSuffix(line, " "+edge), theme)
						assert.Equal(t, width+5, ansi.StringWidth(line), theme)
					}
					assert.Contains(t, ansi.Strip(view), "›", "focused control must remain visible: %s", theme)
					assert.Contains(t, ansi.Strip(view), "Esc", "back must remain visible: %s", theme)
					if theme == "mono" {
						assert.NotContains(t, view, "\x1b", "monochrome must omit style escapes")
					}
				}
			})
		}
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
		columns, rows := m.resultTable()
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
		assert.Contains(t, divider, "38;2;81;86;107")
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

func TestWorkspaceFooterStaysInsideNarrowFrame(t *testing.T) {
	m := modelForTest(t, modelService{})
	m.width, m.height = 40, 12
	m.enter("files")
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	assert.Contains(t, lines[len(lines)-1], "└")
	assert.Less(t, len(lines), m.height)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Enter") || strings.Contains(line, "Esc") {
			found = true
			assert.True(t, strings.HasPrefix(line, " │"))
			assert.True(t, strings.HasSuffix(line, "│"))
			assert.NotContains(t, line, " · ")
		}
		assert.Less(t, ansi.StringWidth(line), m.width)
	}
	require.True(t, found)
}
