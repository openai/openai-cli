package audio

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var waveformCenterline = strings.NewReplacer("·", "─", "│", "┼", "┃", "╂")

func (m *audioWorkbench) waveformStyle() lipgloss.Style {
	if m.focus == 4 && m.waveformFocused && !m.busy {
		return m.accent().Bold(false)
	}
	return m.muted()
}

// Only the waveform's actual focus changes its color; samples never change.
func (m *audioWorkbench) workspaceHairlineRows(wave []string) []string {
	if !m.color {
		return wave
	}
	styles := [...]lipgloss.Style{
		lipgloss.NewStyle(), m.borderStyle(), m.primaryText(), m.waveformStyle(),
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
