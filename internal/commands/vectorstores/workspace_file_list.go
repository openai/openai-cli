package vectorstores

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

const (
	fileUpload  = "Upload file"
	fileRefresh = "Refresh files"
	fileMore    = "Load more files"
	fileBack    = "Back"
)

// Rendering, navigation, and dispatch use the same ordered list.
func (m *workspace) fileActions() []string {
	var actions []string
	if m.page == "files" && m.store != nil && m.store.Status != "expired" {
		actions = append(actions, fileUpload)
	}
	actions = append(actions, fileRefresh)
	if m.fileNext != "" {
		actions = append(actions, fileMore)
	}
	return append(actions, fileBack)
}

func (m *workspace) openFileList(page string) tea.Cmd {
	m.files, m.fileNext, m.filesLoaded = nil, "", false
	m.enter(page)
	return m.loadFiles(false)
}

func (m *workspace) activateFileList() tea.Cmd {
	if m.focus == 0 {
		if m.selected >= len(m.files) {
			return nil
		}
		file := m.files[m.selected]
		m.fileOrigin = m.page
		id := m.store.ID
		return m.start("file-detail", "Reading file details…", func(ctx context.Context) workMessage {
			result, err := m.service.GetFile(ctx, id, file.ID)
			return workMessage{attached: result, err: err}
		})
	}
	switch m.fileActions()[m.focus-1] {
	case fileUpload:
		m.uploadFromFiles = true
		return m.enter("upload")
	case fileRefresh:
		return m.loadFiles(false)
	case fileMore:
		return m.loadFiles(true)
	default:
		return m.back()
	}
}

func (m *workspace) completeFileList(msg workMessage) {
	if msg.reset {
		m.files = nil
		m.selected = 0
	} else if len(msg.files.Files) > 0 {
		m.selected = len(m.files) // Land on the first new file.
	}
	m.files = append(m.files, msg.files.Files...)
	m.fileNext, m.filesLoaded = msg.files.Next, true
	m.focus = 0
	if len(m.files) == 0 {
		m.focus = 1
	}
}

func (m *workspace) fileListLines(lines []string) ([]string, string) {
	actions := m.fileActions()
	footer := "↑↓ files · Tab actions · Enter file menu · Esc back"
	if m.focus > 0 {
		footer = "↑↓ move · Tab next · Enter select · Esc back"
		if len(m.files) > 0 && m.focus == len(actions) {
			footer = "↑↓ move · Tab files · Enter select · Esc back"
		}
	}
	title := "Attached files"
	if m.page == "batchfiles" {
		title = "Batch files"
	}
	lines = append(lines, m.heading(title))
	columns, rows := m.fileTable()
	if len(rows) == 0 {
		message := "No files attached yet."
		if !m.filesLoaded {
			message = "File list unavailable."
		} else if m.page == "batchfiles" {
			message = "No files in this batch."
		}
		lines = append(lines, m.muted().Render(message))
	} else {
		lines = append(lines, m.tableHeader(columns))
	}
	room := m.contentRows(footer) - len(lines) - len(m.feedbackLines()) - 1 // Actions heading.
	actionRows := min(len(actions), max(1, room-min(1, len(rows))))
	if len(rows) > 0 {
		lines = append(lines, m.tableRows(columns, rows, m.selected, max(1, room-actionRows), m.focus == 0)...)
	}
	if m.framed() && len(lines)+actionRows+2+len(m.feedbackLines()) <= m.contentRows(footer) {
		lines = append(lines, "")
	}
	first := max(0, m.focus-actionRows)
	end := min(len(actions), first+actionRows)
	heading := m.heading("Actions")
	if end-first < len(actions) {
		heading += m.muted().Render(fmt.Sprintf("  %d–%d of %d", first+1, end, len(actions)))
	}
	lines = append(lines, heading)
	for i := first; i < end; i++ {
		lines = append(lines, m.row(actions[i], m.focus == i+1))
	}
	return lines, footer
}
