package clihelp

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
)

// addWelcomeEightbit fits a static terminal adaptation beside the existing text.
// The fixed sprite uses no image decoder, file, terminal query, or animation.
func addWelcomeEightbit(content string, width int, profile colorprofile.Profile, dark bool) string {
	if profile <= colorprofile.ASCII {
		return content
	}
	lines := strings.Split(content, "\n")
	if len(lines) != 4 {
		return content
	}
	const spriteWidth = 9
	column := width - spriteWidth
	for _, line := range lines[1:] {
		if ansi.StringWidth(line) >= column {
			return content // Keep every version cell and at least one separating cell.
		}
	}
	// Green goggles, purple head, white eyes, and separated tentacle tips.
	pixels := [...]string{
		"..dgdgd..",
		"..ppppp..",
		".pwwpwwp.",
		".pwdpwdp.",
		".pppdppp.",
		"pp.p.p.pp",
	}
	purple, green, white := "#8345B5", "#438D2D", "#FFFFFF"
	if dark {
		purple, green, white = "#B36BE8", "#8DE84B", "#F2F4FA"
	}
	colors := map[byte]color.Color{
		'p': lipgloss.Color(purple), 'g': lipgloss.Color(green),
		'w': lipgloss.Color(white), 'd': lipgloss.Color("#20283B"),
	}
	for row := 0; row < len(pixels); row += 2 {
		var sprite strings.Builder
		for cell := 0; cell < spriteWidth; cell++ {
			top, bottom := pixels[row][cell], pixels[row+1][cell]
			style := lipgloss.NewStyle()
			switch {
			case top == '.' && bottom == '.':
				sprite.WriteByte(' ')
			case top == '.':
				sprite.WriteString(style.Foreground(colors[bottom]).Render("▄"))
			case bottom == '.':
				sprite.WriteString(style.Foreground(colors[top]).Render("▀"))
			case top == bottom:
				sprite.WriteString(style.Foreground(colors[top]).Render("█"))
			default:
				// Both half-cells are painted pixels; empty half-cells stay inherited.
				sprite.WriteString(style.Foreground(colors[top]).Background(colors[bottom]).Render("▀"))
			}
		}
		i := row/2 + 1
		lines[i] += strings.Repeat(" ", column-ansi.StringWidth(lines[i])) + sprite.String()
	}
	return strings.Join(lines, "\n")
}
