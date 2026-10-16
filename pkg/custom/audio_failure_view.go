package custom

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *audioWorkbench) rememberFailure(operation string, failure error) {
	m.failure = audioFailureFeedback(m.command, operation, failure)
	m.failureNote = m.note
	m.failureOffset = 0
	m.openRecovery()
}

// A later status or validation message owns the visible feedback, even when
// lastErr must remain available for the command's eventual exit status.
func (m *audioWorkbench) activeFailure() bool {
	return m.failure.Summary != "" && m.note == m.failureNote
}

func (m *audioWorkbench) failureSummaryRows() []string {
	width := max(1, m.audioWidth()-2)
	rows := []string{m.strong().Render("! " + ansi.Truncate(readable.Text(m.failure.Summary), width, "…"))}
	action := strings.Split(ansi.Wrap(readable.Text(m.failure.Action), width, ""), "\n")
	for i, line := range action[:min(2, len(action))] {
		if i == 1 && len(action) > 2 {
			line = ansi.Truncate(line, width-1, "") + "…"
		}
		rows = append(rows, "  "+line)
	}
	return rows
}

func (m *audioWorkbench) failureDetailRows() []string {
	values := []string{m.failure.Summary, "", m.failure.Action}
	if m.failure.StatusCode != 0 {
		values = append(values, "", fmt.Sprintf("HTTP status: %d", m.failure.StatusCode))
	}
	if m.failure.RequestID != "" {
		values = append(values, "", "Request ID: "+m.failure.RequestID)
	}
	var rows []string
	for _, value := range values {
		rows = append(rows, strings.Split(ansi.Wrap(readable.Text(value), m.audioWidth(), ""), "\n")...)
	}
	return rows
}

func (m *audioWorkbench) failureDetailHeight() int { return max(1, m.height-5) }

func (m *audioWorkbench) failureView() tea.View {
	rows := m.failureDetailRows()
	height := m.failureDetailHeight()
	start := min(m.failureOffset, max(0, len(rows)-height))
	lines := []string{m.strong().Render("Error details"), ""}
	lines = append(lines, rows[start:min(len(rows), start+height)]...)
	lines = append(lines, "", m.muted().Render("↑↓ scroll · Esc back"))
	return tea.NewView(m.fitAudio(lines))
}

func (m *audioWorkbench) recoveryView() tea.View {
	items := m.recoveryItems()
	lines := strings.Split(ansi.Wrap(m.strong().Render(readable.Text(m.failure.Summary)), m.audioWidth(), ""), "\n")
	lines = append(lines, "")
	lines = append(lines, strings.Split(ansi.Wrap(readable.Text(m.failure.Action), m.audioWidth(), ""), "\n")...)
	// Keep every action reachable when long guidance consumes a small terminal.
	budget := m.height - 2 - len(items)
	if len(lines) > budget {
		lines = lines[:max(1, budget)]
		lines[len(lines)-1] = ansi.Truncate(lines[len(lines)-1], max(1, m.audioWidth()-1), "") + "…"
	}
	lines = append(lines, "")
	for index, item := range items {
		lines = append(lines, m.label(item, m.recoveryChoice == index))
	}
	return tea.NewView(m.fitAudio(lines))
}
