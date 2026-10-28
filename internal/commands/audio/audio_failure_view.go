package audio

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/openai/openai-cli/internal/readable"
)

func (m *audioWorkbench) rememberFailure(operation string, failure error) {
	m.failure = m.failureFeedback(operation, failure)
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
	width := m.failureTextWidth()
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
	values := []string{m.failure.Summary}
	step := 0
	for _, action := range strings.Split(m.failure.Action, "\n") {
		if action == "" {
			continue
		}
		if step == 0 {
			values = append(values, "")
		}
		step++
		values = append(values, fmt.Sprintf("%d. %s", step, action))
	}
	var rows []string
	for _, value := range values {
		rows = append(rows, strings.Split(ansi.Wrap(readable.Text(value), m.failureTextWidth(), ""), "\n")...)
	}
	return rows
}

func (m *audioWorkbench) failureTextWidth() int {
	if m.height < 24 || m.width < 60 {
		return m.audioWidth()
	}
	return max(1, m.audioWidth()-2)
}

func (m *audioWorkbench) failureIndent() string {
	if m.height < 24 || m.width < 60 {
		return ""
	}
	return "  "
}

func (m *audioWorkbench) failureFooter() []string {
	hints := [][2]string{{"Esc", "back"}}
	if len(m.failureDetailRows()) > m.failureDetailHeight() {
		hints = append(hints, [2]string{"↑↓", "scroll"})
	}
	return []string{m.hintLine(hints...)}
}

func (m *audioWorkbench) failureDetailHeight() int {
	return max(1, m.height-5)
}

func (m *audioWorkbench) failureView() tea.View {
	rows := m.failureDetailRows()
	height := m.failureDetailHeight()
	start := min(m.failureOffset, max(0, len(rows)-height))
	lines := []string{m.failureIndent() + m.strong().Render("Error details"), ""}
	for _, row := range rows[start:min(len(rows), start+height)] {
		lines = append(lines, m.failureIndent()+row)
	}
	lines = append(lines, "")
	for _, row := range m.failureFooter() {
		lines = append(lines, m.failureIndent()+row)
	}
	return tea.NewView(m.fitAudio(lines))
}
