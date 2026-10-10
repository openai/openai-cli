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
	const spriteWidth = 15
	column := width - spriteWidth
	// A rounded head, bright eyes, a small mouth, one waving arm, and curled tips.
	pixels := [...]string{
		"......dgdgd....",
		".....ppppppp...",
		"....ppwwpwwpp..",
		".p..ppwdpwdpp..",
		"p...sppppppps..",
		".pppppppdppppp.",
		"..p..p..p...p..",
		".ps.ps..sp..sp.",
	}
	var starts [len(pixels) / 2]int
	for row := range starts {
		for starts[row] < spriteWidth && pixels[2*row][starts[row]] == '.' && pixels[2*row+1][starts[row]] == '.' {
			starts[row]++
		}
		if column < 0 || row+1 < len(lines) && ansi.StringWidth(lines[row+1]) >= column+starts[row] {
			return content // Keep complete text and a gap before the first painted cell.
		}
	}
	// Add room for the curled arms only after every existing line fits.
	lines = append(lines, "")
	purple, green, white, soft := "#8345B5", "#438D2D", "#FFFFFF", "#C18CE3"
	if dark {
		purple, green, white, soft = "#B36BE8", "#8DE84B", "#F2F4FA", "#E0ACF3"
	}
	colors := map[byte]color.Color{
		'p': lipgloss.Color(purple), 'g': lipgloss.Color(green),
		'w': lipgloss.Color(white), 'd': lipgloss.Color("#20283B"), 's': lipgloss.Color(soft),
	}
	for row := 0; row < len(pixels); row += 2 {
		var sprite strings.Builder
		for cell := starts[row/2]; cell < spriteWidth; cell++ {
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
		lines[i] += strings.Repeat(" ", column+starts[row/2]-ansi.StringWidth(lines[i])) + sprite.String()
	}
	return strings.Join(lines, "\n")
}
