package images

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// studioCanvas uses the existing editor height for the empty preview. The outline
// approximates the requested aspect ratio using the normal two-to-one cell shape.
// Actual image previews retain their measured terminal geometry.
func (m *imagePicker) studioCanvas(g imageStudioGeometry, rows int) []string {
	rows = min(rows, g.editorRows)
	if rows <= 0 || g.previewWidth <= 0 {
		return nil
	}
	lines := make([]string, rows)
	lines[0] = m.workspaceText(ansi.Truncate("Preview", g.previewWidth, ""), imageSecondary)
	if rows < 3 {
		return lines
	}
	center := func(text string) string {
		text = ansi.Truncate(text, g.previewWidth, "…")
		return strings.Repeat(" ", max(0, (g.previewWidth-ansi.StringWidth(text))/2)) + text
	}
	width, height, label := 0, 0, ""
	switch m.settings.size {
	case "1536x1024":
		width, height, label = 1536, 1024, "1536 × 1024"
	case "1024x1536":
		width, height, label = 1024, 1536, "1024 × 1536"
	case "1024x1024":
		width, height, label = 1024, 1024, "1024 × 1024"
	default:
		// Auto has no promised dimensions. It must not resemble a square canvas.
		lines[rows/2] = center(m.workspaceText("Automatic size", imageSecondary))
		return lines
	}
	boxRows := min(8, rows-3)
	boxWidth := (boxRows*2*width + height/2) / height
	if limit := min(36, g.previewWidth-4); boxWidth > limit {
		boxWidth = limit
		boxRows = (boxWidth*height + width) / (2 * width)
	}
	if boxRows < 2 || boxWidth < 3 {
		lines[rows/2] = center(m.workspaceText(label, imageSecondary))
		return lines
	}
	top := 2 + max(0, (rows-3-boxRows)/2)
	lines[top] = center(m.workspaceImageBorder("┌" + strings.Repeat("─", boxWidth-2) + "┐"))
	for i := 1; i < boxRows-1; i++ {
		lines[top+i] = center(m.workspaceImageBorder("│" + strings.Repeat(" ", boxWidth-2) + "│"))
	}
	lines[top+boxRows-1] = center(m.workspaceImageBorder("└" + strings.Repeat("─", boxWidth-2) + "┘"))
	lines[top+boxRows] = center(m.workspaceText(label, imageSecondary))
	return lines
}
