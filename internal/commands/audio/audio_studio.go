package audio

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const studioGutter = 3 // One divider cell with space on either side.

func (m *audioWorkbench) studioLayout() bool {
	return m.width >= 110 && m.height >= 28
}

func (m *audioWorkbench) studioScriptWidth() int {
	return min(72, (m.audioWidth()-studioGutter)*45/100)
}

func (m *audioWorkbench) studioMonitorWidth() int {
	return m.audioWidth() - m.studioScriptWidth() - studioGutter
}

// Keep both panes and the command strip mounted as focus moves through the studio.
func (m *audioWorkbench) studioView() tea.View {
	title := "  " + m.strong().Render("Text to speech")
	mode := m.muted().Render("Voice studio") + "  "
	title += strings.Repeat(" ", m.audioWidth()-ansi.StringWidth(title+mode)) + mode
	rows := []string{title, ""}
	rows = append(rows, m.formActionRows()...)
	rows = append(rows, m.listeningRule(), "")

	footer := m.workspaceFooter()
	// Reserve two rows for progress, seek validation, and wrapped shortcuts.
	height := m.audioHeight() - len(rows) - max(2, len(footer))
	left, cursor, focus := m.studioComposerRows()
	width, monitorWidth := m.studioScriptWidth(), m.studioMonitorWidth()
	right, seekCursor := m.studioMonitorRows(monitorWidth, height)
	// In short windows the focused field can sit below its validation or a multiline draft.
	if cursor != nil {
		focus = cursor.Y
	}
	start := max(0, focus-height+1)
	if cursor != nil {
		cursor.Y -= start
	}
	if seekCursor != nil {
		cursor = seekCursor
		cursor.X += width + studioGutter
	}
	if cursor != nil {
		cursor.Y += len(rows)
	}
	seam := " " + m.borderStyle().Render("│") + " "
	for i := range height {
		line := ""
		if start+i < len(left) {
			line = ansi.Truncate(left[start+i], width, "…")
		}
		line += strings.Repeat(" ", width-ansi.StringWidth(line)) + seam
		if i < len(right) {
			line += ansi.Truncate(right[i], monitorWidth, "…")
		}
		rows = append(rows, line)
	}
	return m.cursorView(append(rows, footer...), len(footer), cursor)
}

func (m *audioWorkbench) studioComposerRows() ([]string, *tea.Cursor, int) {
	rows, cursor := m.scriptRows(false)
	delivery := m.focus == 2 && (m.setting < 4 || m.setting == audioSettingDisclosure)
	rows = append(rows, "", m.composerHeading("Delivery", delivery))
	settings, speedCursor, active := m.settingsRows()
	if active >= 0 {
		active += len(rows)
	}
	if speedCursor != nil {
		speedCursor.Y += len(rows)
		cursor = speedCursor
	}
	return append(rows, settings...), cursor, active
}
