package audio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

type translationLayout struct {
	width, height        int
	framed               bool
	title, muted, accent lipgloss.Style
	frame                lipgloss.Style
	divider              string
}

func (m *terminalTranslationModel) translationFramed() bool {
	return m.width >= 60 && m.height >= 16
}

func (m *terminalTranslationModel) translationViewport() (width, height int) {
	if m.translationFramed() {
		return min(100, m.width-7), m.height - 2
	}
	return min(100, max(1, m.width-4)), m.height
}

func (m *terminalTranslationModel) translationLayout() translationLayout {
	width, height := m.translationViewport()
	layout := translationLayout{
		width: width, height: height, framed: m.translationFramed(),
		frame: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1),
	}
	layout.title, layout.muted, layout.accent = m.styles()
	fill, edge, secondary := "#E9EFFE", "#BAC4D8", "#657087"
	if m.dark {
		fill, edge, secondary = "#343D58", "#51566B", "#A4ACC2"
	}
	if m.color {
		layout.frame = layout.frame.BorderForeground(lipgloss.Color(edge))
	}
	layout.divider = cliterminal.SectionDivider(width, m.color, fill, edge, secondary)
	return layout
}

func (m *terminalTranslationModel) styles() (lipgloss.Style, lipgloss.Style, lipgloss.Style) {
	title, muted, accent := lipgloss.NewStyle(), lipgloss.NewStyle(), lipgloss.NewStyle()
	if m.color {
		title = title.Bold(true)
		blue, secondary := "#3159BC", "#657087"
		if m.dark {
			blue, secondary = "#8AA8FF", "#A4ACC2"
		}
		muted = muted.Foreground(lipgloss.Color(secondary))
		accent = accent.Foreground(lipgloss.Color(blue))
	}
	return title, muted, accent
}

func (l translationLayout) heading(left, right string) string {
	if right == "" {
		return left
	}
	left = ansi.Truncate(left, l.width-ansi.StringWidth(right)-2, "…")
	return left + strings.Repeat(" ", l.width-ansi.StringWidth(left)-ansi.StringWidth(right)) + right
}

func (l translationLayout) footer(pairs ...[2]string) []string {
	var rows []string
	separator := l.muted.Render(" · ")
	var line string
	for _, pair := range pairs {
		item := l.accent.Render(pair[0]) + " " + l.muted.Render(pair[1])
		if line != "" && ansi.StringWidth(line+separator+item) > l.width {
			rows = append(rows, line)
			line = ""
		}
		if line != "" {
			line += separator
		}
		line += item
	}
	if line != "" {
		rows = append(rows, line)
	}
	return rows
}

func (l translationLayout) panel(rows, footer []string, cursor *tea.Cursor) tea.View {
	rows = rows[:min(len(rows), max(0, l.height-len(footer)))]
	for i, row := range rows {
		rows[i] = ansi.Truncate(row, l.width, "…")
		if l.framed {
			rows[i] += strings.Repeat(" ", l.width-ansi.StringWidth(rows[i]))
		}
	}
	content := strings.Join(rows, "\n")
	if l.framed {
		content = l.frame.Render(content)
	} else {
		content = "  " + strings.ReplaceAll(content, "\n", "\n  ")
	}
	for _, row := range footer {
		content += "\n  " + ansi.Truncate(row, l.width, "…")
	}
	view := tea.NewView(content)
	if cursor != nil && cursor.Y < len(rows) {
		position := *cursor
		position.X += 2
		if l.framed {
			position.Y++
		}
		view.Cursor = &position
	}
	return view
}

func (m *terminalTranslationModel) View() tea.View {
	if m.width < 40 || m.height < 16 {
		return tea.NewView(ansi.Truncate("Resize to 40 × 16. Space stops; Ctrl+C exits.", max(1, m.width-1), "…"))
	}
	layout := m.translationLayout()
	switch {
	case !m.started:
		return m.setupView(layout)
	case m.transcript != nil:
		return m.transcriptView(layout)
	default:
		return m.resultView(layout)
	}
}
