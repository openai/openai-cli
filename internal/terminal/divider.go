package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// SectionDivider renders the Batch browser's thin, center-bright rule.
func SectionDivider(width int, colors bool, fill, edge, secondary string) string {
	if width <= 0 {
		return ""
	}
	if !colors {
		return strings.Repeat("─", width)
	}
	shades := lipgloss.Blend1D((width+1)/2, lipgloss.Color(fill), lipgloss.Color(edge), lipgloss.Color(secondary))
	var rule strings.Builder
	for i := range width {
		rule.WriteString(lipgloss.NewStyle().Foreground(shades[min(i, width-1-i)]).Render("─"))
	}
	return rule.String()
}
