package audio

import (
	"strings"

	"charm.land/lipgloss/v2"
)

var waveformCenterline = strings.NewReplacer("·", "─", "│", "┼", "┃", "╂")

func (m *audioWorkbench) waveformInk(light, dark string) lipgloss.Style {
	style := lipgloss.NewStyle()
	if !m.color {
		return style
	}
	if m.dark {
		return style.Foreground(lipgloss.Color(dark))
	}
	return style.Foreground(lipgloss.Color(light))
}

func (m *audioWorkbench) elapsedWaveformStyle() lipgloss.Style {
	return m.waveformInk("#157656", "#51C89D")
}

func (m *audioWorkbench) workspaceHairlineRows(wave []string, marker int) []string {
	if !m.color {
		return wave
	}
	const (
		plain = iota
		axis
		playhead
		upcoming
		played
		upcomingTip
		playedTip
	)
	styles := [...]lipgloss.Style{
		plain:       lipgloss.NewStyle(),
		axis:        m.borderStyle(),
		playhead:    m.playheadStyle(),
		upcoming:    m.muted(),
		played:      m.elapsedWaveformStyle(),
		upcomingTip: m.waveformInk("#576174", "#8F96A8"),
		playedTip:   m.waveformInk("#116449", "#44A985"),
	}
	grid := make([][]rune, len(wave))
	for row, line := range wave {
		grid[row] = []rune(line)
	}
	rows := make([]string, len(wave))
	middle := len(grid) / 2
	for row, cells := range grid {
		// Shade one terminal cell at each end of a bar at least five rows tall.
		// Shorter bars keep their core color at this resolution.
		tintTips := row <= middle-2 || row >= middle+2
		var outward []rune
		if row > 0 && row < middle {
			outward = grid[row-1]
		} else if row > middle && row+1 < len(grid) {
			outward = grid[row+1]
		}
		var text strings.Builder
		start, previous := 0, plain
		for column, glyph := range cells {
			role := plain
			switch glyph {
			case '─', '·', '╶', '╴':
				role = axis
			case '┃', '╂', '┠', '┨':
				role = playhead
			case '│', '╷', '╵', '┼':
				tip := tintTips && (outward == nil || outward[column] == ' ')
				switch {
				case column < marker && tip:
					role = playedTip
				case column < marker:
					role = played
				case tip:
					role = upcomingTip
				default:
					role = upcoming
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
