package audio

import (
	"math"
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

func (m *audioWorkbench) workspaceHairlineRows(wave []string, marker int, levels []float64) []string {
	if !m.color {
		return wave
	}
	const (
		plain = iota
		axis
		playhead
		upcoming
	)
	const shades = 8
	const played = upcoming + shades
	styles := [played + shades]lipgloss.Style{
		plain:    lipgloss.NewStyle(),
		axis:     m.borderStyle(),
		playhead: m.playheadStyle(),
	}
	// Use the same perceptual blend as the section divider, without painting the background.
	setRamp := func(start int, tip, core lipgloss.Style) {
		for i, ink := range lipgloss.Blend1D(shades, tip.GetForeground(), core.GetForeground()) {
			styles[start+i] = lipgloss.NewStyle().Foreground(ink)
		}
		styles[start], styles[start+shades-1] = tip, core
	}
	setRamp(upcoming, m.waveformInk("#435067", "#737C91"), m.muted())
	setRamp(played, m.waveformInk("#104F3B", "#326F5C"), m.elapsedWaveformStyle())
	rows := make([]string, len(wave))
	middle := len(wave) / 2
	for row, line := range wave {
		cells := []rune(line)
		distance := 2 * max(middle-row, row-middle) // half-cells from the centerline
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
				role = upcoming
				if column < marker {
					role = played
				}
				shade := shades - 1
				if distance >= 4 {
					// Fade the last third of each half-bar. The first visible tip
					// appears when rounding reaches the next half-cell.
					level := levels[column-1] // column zero belongs to the timeline endpoint.
					inset := level - float64(distance) + 0.5
					fraction := min(1, max(0, inset/max(2, level/3)))
					shade = int(math.Round(fraction * (shades - 1)))
				}
				role += shade
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
