package audio

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var waveformCenterline = strings.NewReplacer("·", "─", "│", "┼", "┃", "╂")

// Measured strokes before the playhead use blue, regardless of keyboard focus.
func (m *audioWorkbench) workspaceHairlineRows(wave []string, marker int) []string {
	if !m.color {
		return wave
	}
	styles := [...]lipgloss.Style{
		lipgloss.NewStyle(), m.borderStyle(), m.playheadStyle(), m.muted(), m.accent().Bold(false),
	}
	rows := make([]string, len(wave))
	for row, line := range wave {
		cells := []rune(line)
		var text strings.Builder
		start, previous := 0, 0
		for column, glyph := range cells {
			role := 0
			switch glyph {
			case '─', '·', '╶', '╴':
				role = 1
			case '┃', '╂', '┠', '┨':
				role = 2
			case '│', '╷', '╵', '┼':
				role = 3
				if column < marker {
					role = 4
				}
			}
			if role != previous {
				if column > start {
					text.WriteString(styles[previous].Render(string(cells[start:column])))
				}
				start, previous = column, role
			}
		}
		if start < len(cells) {
			text.WriteString(styles[previous].Render(string(cells[start:])))
		}
		rows[row] = text.String()
	}
	return rows
}
