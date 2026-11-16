package images

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	cliterminal "github.com/openai/openai-cli/internal/terminal"
)

// Give the prompt field a quiet surface without changing its text or cell width.
func (m *imagePicker) studioSurfaceRow(line string, width int) string {
	if width < 1 {
		return ""
	}
	line = cliterminal.FitPrompt(line, width)
	if !m.color || m.profile < colorprofile.ANSI256 {
		return line
	}
	// The palette's fill is reserved for selection. These neutral surfaces keep
	// the larger inspector quiet and retain contrast in both terminal themes.
	background := "#F3F4F7"
	if m.dark {
		background = "#242732"
	}
	return lipgloss.NewStyle().
		Foreground(m.profile.Convert(lipgloss.Color(m.palette().selected))).
		Background(m.profile.Convert(lipgloss.Color(background))).
		Render(line)
}
