package terminal

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/xo/terminfo"
)

// SupportsDECLineDrawing reports whether the terminal advertises DEC's native strokes.
func SupportsDECLineDrawing() bool {
	ti, err := terminfo.LoadFromEnv()
	return err == nil &&
		string(ti.Strings[terminfo.EnterAltCharsetMode]) == ansi.SelectCharacterSet('(', '0') &&
		string(ti.Strings[terminfo.ExitAltCharsetMode]) == ansi.SelectCharacterSet('(', 'B')
}

// PaneDivider pins the stroke to a one-based column even when terminals disagree about emoji widths.
func PaneDivider(column int, border lipgloss.Style, dec bool) string {
	stroke := "│"
	if dec {
		stroke = ansi.SelectCharacterSet('(', '0') + "x" + ansi.SelectCharacterSet('(', 'B')
	}
	return ansi.CursorHorizontalAbsolute(column) + border.Render(stroke)
}

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
